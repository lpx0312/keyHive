# 存储与模板（store/templates）代码审查报告

> 审查日期：2026-10-28 · 审查人：代码审查 agent · 只读审查
> 范围：`internal/store/`（store.go、store_test.go、store_extra_test.go）、`internal/templates/`（templates.go、templates_test.go）
> 交叉引用（仅为验证结论，非审查对象）：internal/api、internal/crypto、internal/db、internal/model、cmd/keyhive/main.go

## 概览

范围共 5 个文件、约 1100 行。store.go（389 行）承载全部条目/模板持久化、字段加解密编排与主密钥轮换；templates.go 是 32 个内置模板的纯数据定义。总体评价：加密编排集中在 store 单点、轮换的"预计算→事务→成功后换钥"顺序正确、关键安全属性（落库即密文、密文不可搜、模板不带值）都有测试锚定，工程质量高于典型自托管项目。主要风险集中在三处：**轮换流程的完整性**（遗漏 settings 表中另一份密文）、**轮换与并发写入的竞态窗口**（可造成不可恢复的旧密钥密文）、以及**单条坏数据无隔离的放大效应**（一条坏行拖垮整库列表且无法经 API 删除）。模板数据本身干净，未见注入风险。

发现计数：**P0=0，P1=3，P2=3，P3=6**。

## 发现清单

### P1（严重）

**[P1] 主密钥轮换遗漏 settings 表中的 LLM API Key 密文，轮换后 AI 助手不可用**（store.go:316-389）
- `RotateAllEntries` 只重加密 `entries` 表并更新 `key_check`。但项目里还存在第二类用同一 `Cipher` 加密的数据：LLM API Key（`aichat_api.go:109` 以 `s.Store.Cipher.Encrypt(...)` 写入 settings 键 `ai_llm_api_key`，见 aichat_api.go:15-21 的常量定义）。轮换提交后内存密钥换新，这份密文仍为旧钥密文，`Decrypt` 必然失败——aichat_api.go:145 的错误信息"API Key 解密失败（主密钥变更？）"说明该情形已被预见到但未处理。
- 影响：管理员执行一次正常的密钥轮换后，AI 聊天、AI 配置测试全部 500，已存 API Key 密文失联，只能重新录入。属于"轮换未覆盖全部密文资产"的完整性缺陷（审查关注点 1/5）。
- 修复建议：在 `RotateAllEntries` 的事务内同步对 settings 中的密文键（至少 `ai_llm_api_key`）做"旧钥解密→新钥加密"重写；或把 settings 密文的加解密也收编进 store 抽象，使轮换持有完整的密文资产清单。

**[P1] 轮换全程不持写锁：窗口内并发写入的条目留下旧密钥密文，永久不可解密**（store.go:318-389）
- 流程分四步：读全表（320-340）→ 内存预计算（342-358）→ 事务落库（364-382）→ 换内存密钥（385-387）。`s.mu` 仅在第 4 步短暂持有；而 `CreateEntry`/`UpdateEntry`（store.go:112、139）全程不加写互斥，只在取 `cipher()` 时 RLock。
- 竞态：轮换读表之后、事务提交之前，另一请求新建/更新的条目用**旧密钥**加密落库，且不在 `updates` 名单中；事务提交、`s.Cipher` 换新、密钥文件被新钥覆盖（api/rotate.go:49）后，该条目密文**永久不可恢复**（旧钥已丢弃），并连带触发下一条 P1 的整库列表失败。`db.SetMaxOpenConns(1)` 只串行化单条 SQL，无法消除这个读-算-写窗口（TOCTOU）。大库轮换耗时数秒，窗口并不瞬态；导入（webui.go:108/143 逐条 CreateEntry）与 AI 草稿入库都可能撞上。
- 修复建议：轮换全程持 `s.mu.Lock()`（轮换内部改用显式 cipher 参数解密，避免与 `s.cipher()` 的 RLock 死锁——可把 `decryptFields` 重构为接收 cipher 的私有函数）；同时让 `CreateEntry`/`UpdateEntry`/`DeleteEntry` 整个函数体持 `s.mu.RLock()`，读写互斥即闭合窗口。

**[P1] 单条损坏条目无隔离：一条坏数据令整库列表/导出/AI 检索全部失败，且该条目无法经 API 删除**（store.go:204-211）
- `ListEntries` 在循环内 `scanEntry`，任一行 JSON 损坏或密文解密失败即整体 `return nil, err`（205-208）——一条坏行使全库不可列出，连带 `/export`（api.go:115-125）与 AI 检索全部不可用。
- 更糟的是删除通道也被堵死：api.go:309-313 的 `deleteEntry` 先 `GetEntry`，解密失败被当成 404"条目不存在"；`getEntry`（api.go:271-274）同理。坏条目既读不出、也删不掉，只能直接操作数据库文件恢复。触发路径并非只有手工改库：下一条 P2 的 `enc:v1:` 前缀陷阱可经**正常写路径**制造坏行。
- store_extra_test.go:255-257（"列表扫描坏数据应报错"）实际上把该行为断言成了预期语义。
- 修复建议：`ListEntries`/导出对单行解密失败做降级（跳过该行并在结果中标记/记日志），并提供坏行强制删除或隔离通道（`DELETE ... WHERE id=?` 不依赖先读）。

### P2（一般）

**[P2] 敏感明文以 "enc:v1:" 开头会被跳过加密原样入库，读回必然解密失败（条目自我砖化）**（store.go:48）
- `encryptFieldsWith` 对 `crypto.IsEncrypted(Value)` 为真的敏感值直接跳过加密（"兼容手工导入"的幂等设计），但判断仅看字符串前缀。若用户存入的**真实明文**恰好以 `enc:v1:` 开头（例如从别的 keyHive 库/导出文件里复制来的密文串、或以该前缀开头的口令），它会以"看似密文"的形态明文落库；读取时 `decryptFields`（store.go:69）对带前缀串强制走 `Decrypt`，base64 解码或 GCM 校验必失败——CreateEntry 成功、GetEntry 永久失败，并经上一条 P1 放大为整库不可用。这是"写路径漏加密"的一个真实可达路径（审查关注点 1）。
- 修复建议：跳过加密前先用**当前密钥**尝试 `Decrypt` 验证确实可解，验证失败则强制再加密一层（前缀转义）；或取消跳过逻辑，改为调用方负责传明文。

**[P2] API 层绕过 cipher() 访问器直接读写导出字段 Cipher，与轮换存在数据竞争**（store.go:19-34；aichat_api.go:61、109、143、226、348、380）
- Store 特意以 RWMutex + 私有 `cipher()`（store.go:30-34）保护 `Cipher` 的并发替换，但字段本身是导出的，aichat_api 六处直接 `s.Store.Cipher.Encrypt/Decrypt`。轮换第 4 步写 `s.Cipher` 与这些读之间是无同步的指针并发读写（`-race` 可检出），并可能在换钥瞬间用旧钥加密新写入的 LLM Key，叠加上面 P1-1 的失效。
- 修复建议：将 `cipher()` 提升为导出访问器（如 `CipherNow()` 持 RLock），字段改小写；API 层一律走访问器。

**[P2] UpdateEntry 读-改-写无并发控制，并发更新整行互相覆盖**（store.go:139-168）
- 先 `GetEntry` 取旧值（供 `***` 哨兵还原，147-154），再整行 UPDATE 覆写全部字段。两个并发更新都基于同一旧快照时，后提交者整体覆盖前者（含 fields 全量覆写），无版本号/乐观锁检测，静默丢失更新。共享库多用户同时编辑同一条目时可触发（HTTP handler 天然并发，`MaxOpenConns(1)` 不阻止两个 GetEntry 都先完成）。
- 修复建议：以 `updated_at` 做乐观锁（`WHERE id=? AND updated_at=?`，0 行受影响即 409），或对条目写路径加互斥。

### P3（建议）

**[P3] 搜索 LIKE 通配符未转义；大小写折叠仅 ASCII**（store.go:188-190）
- 用户搜索词中的 `%`/`_` 按通配符解释："100%"会匹配一切含"100"的行、"a_b"命中 aXb，结果过宽。SQLite `LIKE` 仅对 ASCII 大小写不敏感，非 ASCII 字母的大小写不等价（中文无影响）。参数化查询，无注入风险，纯准确度问题。建议转义 `%`/`_` 并加 `ESCAPE`，必要时文档化 ASCII-only 折叠。

**[P3] fields 列对原始 JSON 做 LIKE，常见词全库命中、搜索语义过宽**（store.go:188）
- 对 fields 的匹配对象是整段 JSON 文本：搜 `text`/`url`/`key`/`value`/`is_secret`/`description`/`enc` 等词会命中所有（或大量）条目（每条 JSON 都含 `"type":"text"` 等结构字样）；字段 Key 与给 AI 看的 Description 也一并纳入"值搜索"。产品定位的五路搜索（标题/描述/字段名/URL/标签）实际是"整段 JSON 模糊匹配 + category"。建议用 `json_each` 提取 key/value 精确匹配，或至少文档化该行为。

**[P3] CreateEntry 接受敏感值 "***" 原样入库**（store.go:112-136）
- 遮蔽哨兵只在 `UpdateEntry`（147-154）处理；直接创建时（前端遮蔽回显误提交、AI 草稿回传 `***`——aichat 提示词明确允许回传 `***`，若 LLM 在创建场景误用则会入库），`***` 被当真实密码加密保存，之后 reveal 显示与遮蔽态无法区分。建议 CreateEntry 对 `IsSecret && Value==***` 视为空或报错。

**[P3] SaveTemplate 不校验字段合法性；UNIQUE 冲突原样抛 SQLite 错误**（store.go:283-287、301-302）
- 模板字段只 `Normalize`，不检查空 Key/重复 Key/非法 Type（条目侧 `Validate` 在 model.go:90-117 有完整检查）。坏模板创建的条目会在 `CreateEntry` 才报"字段名重复/类型无效"，用户难以定位到模板。另外 `saveAsTemplate`（api.go:341）用 `e.Category+"_tpl"` 作 category，同分类条目二次另存必撞 `category_templates.category` UNIQUE，前端收到原始 constraint 错误文案。建议模板保存复用字段校验并本地化错误。

**[P3] DeleteTemplate 对不存在的 id 静默成功**（store.go:311-314）
- 不检查 RowsAffected（对比 SaveTemplate 的"模板不存在"，store.go:296-298），调用方无法区分"已删"与"从未存在"，行为不一致。建议检查 RowsAffected 或明确幂等语义并注释。

**[P3] 内置模板可被任意登录用户（非管理员）改写，注释内容将进入 AI 上下文**（store.go:290-299；api.go:78-81）
- store.go:290 注释明确"内置模板允许编辑"是有意设计，`DeleteTemplate` 也以 `builtin=0` 正确防删；但路由上 POST/PUT `/templates` 只要求会话（api.go:78-81，写操作挂在非 admin 组）。模板字段 Description 正是 kh_list/kh_search 呈现给 LLM 的"字段注释"（templates.go 的设计意图），多用户部署中非管理员可改写全局模板骨架影响他人录入，并构成对 AI 会话的提示注入面。共享库设计中条目本身也是全员可写，故仅列为建议：内置模板（builtin=1）的写操作限管理员。

## 做得好的地方

1. **加密编排单点集中**：所有条目写路径（Create/Update、Web 导入 webui.go:108/143）统一经 `encryptFields` 落库，读路径统一解密；`enc:v1:` 前缀 + `key_check` 设计让"密钥不匹配"在启动时即可检测（main.go:146），而不是等读到坏数据才发现。`TestSecretsEncryptedAtRest` 直接查库断言密文形态，锚定可靠。
2. **轮换的正确顺序**：预计算全部新密文→单事务落库→失败回滚旧钥仍有效→成功后才换内存密钥（store.go:316-389），空库轮换、坏数据中断、关库错误路径均有测试覆盖（store_test.go:151-199、store_extra_test.go:206-298）。
3. **模板只存骨架**：SaveTemplate 强制清空全部字段值（store.go:285），从条目另存模板（api.go:326-354）不携带任何明文；DeleteTemplate 以 `builtin=0` 防止内置模板被删；种子数据 `INSERT OR IGNORE` 且仅首启执行，升级不覆盖用户改动。
4. **搜索全程参数化**，无 SQL 注入；"敏感值密文天然不可搜"有专门测试锚定（store_extra_test.go:68-71），且密文不可搜是加密设计的自然结果而非事后过滤。
5. **JSON 兼容细节统一处理**：空结果初始化为 `[]` 而非 nil（store.go:202/221/254，含注释说明前端 `null` 崩溃原因），并有测试断言；`encryptFieldsWith` 先 copy 再改（store.go:45-46），不污染调用方持有的明文结构。

## 测试盲区

- **并发完全未测**：轮换与 Create/Update 并发（P1-2 的窗口，可用 `-race` 驱动）、UpdateEntry 并发覆盖（P2-3）、轮换期间 LLM Key 读写（P2-2）均无测试。
- **settings 密文的轮换后行为无跨包测试**（P1-1）：现有 rotate 测试只断言 entries 与 key_check。
- **"enc:v1:" 前缀明文的往返未测**（P2-1）；单条坏数据的"隔离/降级"无测试——TestCorruptAndClosedDB 反而把整体失败锁定为预期（P1-3）。
- LIKE 的 `%`/`_` 转义、ASCII-only 大小写、JSON 结构噪声（P3-1/2）无测试；`ai_visible` 在 store 层无语义测试（过滤完全依赖 aiapi/aichat 层，新增调用方漏过滤无兜底信号）。
- CreateEntry 的 `***` 值、模板字段非法（空 Key/重复 Key）、DeleteTemplate 幂等、UNIQUE 冲突的错误文案，均无测试。
- templates_test.go:47 的分组检查只覆盖 8 组中的 5 组（缺 GroupMiddleware/GroupDevOps/GroupCert），这三组整体缺失不会被发觉。
- `deleted_at`/软删除：store 为硬 DELETE（store.go:176-179），审计日志保留 title 作追溯。当前语义自洽，但若未来引入软删除，现有测试全无对应语义可依赖。
