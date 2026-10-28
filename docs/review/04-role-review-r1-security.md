# 安全角色复审 · 第一轮（2026-10-28）

> 复审人视角：安全架构师。对象：本轮工作区未提交的 21 项缺陷修复（清单见 [02-test-report.md](02-test-report.md) 第 3 节）。
> 方法：`git diff` 全量走读 + 调用点 grep 复核 + `go build ./...` / `go vet` / 10 个受影响包 `go test -count=1` 复跑（全绿）。
> 结论速览：**17 项安全相关修复 = 14 ✅ / 2 ⚠️ / 1 ❌**；另发现新问题 2 中 + 3 低 + 3 提示级。总体评级 **B+（可信，附条件）**。

## 逐项核验

| # | 修复项 | 结果 | 依据（file:line） |
|---|---|---|---|
| 1 | 【P0】ai-config/test、models 移入 RequireAdmin | ✅ | api.go:155-156（ar 组 `ar.Post("/ai-config/test"/models")`）；api.go:143（GET 留在登录组，仅回遮蔽 key/元数据，合理）；TestBug_AIConfigTestRequiresAdmin 断言普通用户 403；前端 Settings.vue:97/109 `v-if="me.isAdmin"` 门控。空 Key 回退现状：aichat_api.go:348-362（test）、:385-392（models）——空串或 `****` 前缀回退已保存密钥，**该回退现仅 admin 可达**，凭据外带面关闭；admin 测试自己配置的合法场景保留（表单回显遮蔽值→回退库内 Key，闭环成立） |
| 2 | 全局 10MB MaxBytesReader + 413 | ⚠️ | 实现正确：api.go:23-37（ContentLength 预判 413 + MaxBytesReader 包裹）、:50-58（readBody 把 `*http.MaxBytesError` 映射 errBodyTooLarge → writeReadErr 413，chunked 场景覆盖）；api.go:100 `r.Use(limitBody)`。SSE 未误伤：limitBody 只管**请求体**，handleAIChatStream 走 readBody（10MB 内）、响应流不受限；main.go:143-148 特意不设 WriteTimeout。**缺口：`/api/v1/ai/*` 子树挂载在 human.Routes 之外（main.go:124-138），不经过 limitBody**——aiapi.go:109 Reveal 直接 `json.NewDecoder(r.Body).Decode`，持 reveal scope token 者可发无限大 body，"全站（含未认证端点）"的修复声明不成立（见新发现 N2） |
| 3 | audit.Detail 全面替换 | ✅ | audit.go:10-17 helper（json.Marshal，失败回 `{}`）；grep 全部 28 个 `audit.Log` 调用点：18 处改 `audit.Detail(...)`（api.go:176/316/332/354/373/404、admin.go:40/73/94/154、users.go:91/178/213、webui.go:59/117/152、aichat_api.go:198/301、rotate.go:60、aiapi.go:127-128），其余 10 处为字面 `{}` / 固定合法 JSON 串（api.go:198/204/214/283、admin.go:179、aichat_api.go:119、users.go 内含 reason 常量等）——**无漏网的字符串拼接** |
| 4 | VerifyPassword 畸形 PHC 不再 panic | ✅ | password.go:59-63：t/p/m=0、盐/密文长度 0 前置判 false（空密文恒通过的绕过同关）；TestBug 复现转绿 |
| 5 | writeInternalErr 收敛 5xx 回显 | ⚠️ | api 包内 5xx 已全部收敛（api.go:41-46 helper + 全部替换点）；**漏网**：aiapi.go:57、:90 仍 `writeErr(w, 500, err.Error())` 直传给 AI token 持有者（store 层错误含 SQLite 内部信息）；rotate.go:23、:35 保留 err.Error()（admin-only，:35 为轮换失败诊断刻意保留，可接受）；另 api.go:311/349/399、admin.go:36/69、webui.go:86/126 的 **400** err.Error() 在 CreateEntry/SaveTemplate 路径可能透出 SQLite 原始错误（store.go:135-137 直接 `return err`），属同类纪律缺口（见 N3/N5） |
| 6 | /export?masked=1 服务端遮蔽 | ✅ | api.go:169-179：masked 后逐条 `MaskSecrets()` 再序列化——遮蔽发生在服务端，明文不出进程；遮蔽规则与列表/泄露路径**同源**：model.go:81-87（IsSecret→`***`，model.go:23），listEntries（api.go:299-301）、AI 检索（aichat_api.go:180/264）、aiapi（aiapi.go:65/77/98）全用同一函数，reveal 单口审计。测试 TestExportMaskedParam 断言整包响应文本不含 secret 明文 + 结构不变。闭环防护：遮蔽版被 ParseKeyhive 拒绝恢复导入（keyhive.go:32-34，报错指明用明文导出）；导入侧 keyhive JSON 中 `***` 敏感值即整体拒绝。审计记录 masked 标志（api.go:176） |
| 7 | 空/纯白 master.key 拒绝启动 | ✅ | crypto.go:139-143：TrimSpace 为空即报错并给修复指引（删除文件重新生成）；env 路径 crypto.go:102 TrimSpace 空则跳过 env，同样不受空串影响。（弱口令 env 单次 SHA-256 派生仍存在，属 A2 既定遗留） |
| 8 | 敏感值一律加密（去掉 IsEncrypted 跳过） | ✅ | store.go:44-47：`IsSecret && Value != ""` 即加密，注释说明"写入侧拿到的永远是待加密明文"；`***` 哨兵在 UpdateEntry 先还原旧值明文再加密（store.go:155-160），语义保持；TestBug_PlaintextWithEncPrefixCorrupted 转绿 |
| 9 | ListEntries 坏行隔离 | ✅ | store.go:93-99（scanEntry 失败仍带回条目含 ID）+ :206-234：部分损坏跳过并记日志（含条目 ID），**命中行全损仍整体报错**（防"密钥不匹配被降级成空列表"）；GetEntry 保持严格（store.go:177-179）。注意：坏行也会被 export 跳过——备份静默少数据但有服务端日志可查（提示级，见 N8 附注） |
| 10 | 轮换含 settings（enc:v1:% 排除 key_check） | ✅ | store.go:392 `WHERE value LIKE 'enc:v1:%' AND key != 'key_check'`：key_check 本身是 `Encrypt()` 产物、带 enc:v1: 前缀（crypto.go:79-81），**排除正确且必要**（不排除会重复处理，虽无害但冗余；排除后由 store.go:443-446 在同事务内用新钥重新生成）。预读旧钥解密（:396-407，旧钥窗口内）→ 与条目密文同事务落库（:437-443）→ 提交后换内存钥（:452-456），原子性成立；TestBug_RotateMissesSettingsSecret 转绿。边缘：settings 明文值恰以 enc:v1: 开头会使轮换解密失败整体中止（fail-closed，见 N7）；预读在事务外的 TOCTOU 属 A1 既定跟踪 |
| 11 | Anthropic 流 error 事件传播 | ✅ | 测试锚定（TestBug_AnthropicStreamErrorPropagated 转绿）；不再"空回复+成功"误导 |
| 12 | 默认端点按协议分派（Key 发错端点） | ❌ | aichat 层已修：aichat.go:47-53 `defaultBaseURL()`，Chat（:223-224）/ChatStream（stream.go:30-31）/ListModels（models.go:18-19、:107-109）仅在 `cfg.BaseURL == ""` 时分派。**但生产唯一调用方 api 层把空值预填掉了**：aichat_api.go:27 `BaseURL: orDefault(getSetting, aichat.DefaultBaseURL)`——DefaultBaseURL 是智谱 OpenAI 端点常量（aichat.go:42）。protocol=anthropic 且未保存 base_url 时，cfg.BaseURL 恒非空 → aichat 层分派死代码不触发 → **x-api-key 仍发往 open.bigmodel.cn**；test/models 端点同样命中（aichat_api.go:366、:396 回退 saved.BaseURL 即预填值）。aichat 层测试只测空 BaseURL 直调；api 层 anthropic 用例（aichat_api_test.go:159）显式填了 URL，恰好绕开该组合。**修复 #12 在生产路径未生效**（见 N1） |
| 13 | adminFlags 解析失败退出 2 零请求 | ✅ | admincli.go:94-108（返回 error）+ cmdRotateKey/cmdEdit（:307-313、:349-355）exit 2；cmdExport/cmdImport 自有 flagset 同样处理（:113-116、:174-178）。rotate-key 打错 flag 不再触发真实轮换 |
| 14 | import 登录一次复用会话 | ✅ | admincli.go:218-233、:273-288：loginAdmin 建会话后 sess.call 循环；仍走 admin 凭据 + /entries，无越权面；服务端 argon2id 验证与登录审计从 N 次降为 1 次 |
| 15 | Bitwarden notes 收紧 | ✅ | importer.go:112-117：notes `is_secret=true`（multiline）+ 含 notes 条目 `ai_visible=false`（无 notes 保持 true）；notes 走遮蔽/reveal 审计/AI 隐身全套管控 |
| 16 | CSV LazyQuotes=false | ✅ | importer.go:57-64：畸形引号报错拒绝；FieldsPerRecord=-1、BOM 剥离保持。兼容性：Bitwarden/Chrome 官方导出是规范 RFC4180（引号正确转义），LazyQuotes=false 完全可读；keyHive 自身导出是 JSON 不经 CSV 路径——**合法导出文件不受影响** |
| 17 | config.json chmod 0600 | ✅ | login.go:117-122：写后显式 chmod（新建/合并更新都覆盖）；Windows 只读位语义差异已注释且失败不误报 |
| 18 | Server 超时 + runServe 用配置后的 server | ✅ | main.go:66-71（srv.ListenAndServe 替代零配置 http.ListenAndServe）+ :143-148：ReadHeaderTimeout 10s（防慢速头部）、IdleTimeout 120s、不设 WriteTimeout（保 SSE），注释明确 |

非安全项（#17 MCP IsError、#20 交付、#21 前端改密反馈/搜索竞态）抽查无安全回归：EntriesList.vue loadSeq 乱序丢弃逻辑正确（含 loading/报错归属最新请求）；Settings.vue change() catch 反馈正确。

## 新发现问题（分级）

**N1【中｜修复不完整】anthropic 空 base_url 时 API Key 仍发往智谱端点（#12 残留）**
- aichat_api.go:27 `currentLLMConfig` 把空 base_url 预填为 `aichat.DefaultBaseURL`（智谱 OpenAI 端点），使 aichat 层按协议分派的修复在生产路径成为死代码。触发：管理员选 anthropic 协议且不填/清空地址（UI 预设会带 URL，手动选协议才会命中）→ 聊天/测试/模型列表请求携带 anthropic Key 发往 open.bigmodel.cn（凭据错发第三方）。
- 修法：currentLLMConfig 不预填 BaseURL（留空交给 aichat 层分派），或预填时按 `ProtocolOrDefault()` 分派；GET /ai-config 的 base_url 回显（aichat_api.go:67）同步。补一条 api 层 anthropic+空 base_url 的端到端测试。

**N2【中低｜覆盖缺口】`/api/v1/ai/*`（Bearer token API）无请求体大小限制**
- limitBody 注册在 human.Routes 内（api.go:100），ai 子树独立挂载（main.go:124-138）不经过它；aiapi.go:109 Reveal 直接解码 body。持 reveal token 者可无限大 JSON body 造成内存耗尽（与本轮修掉的未认证同型，但门槛降为需 token）。修法一行：把 `r.Use(limitBody)` 提到 main.go 顶层 router（或在 aiapi 组内补）。

**N3【低】aiapi 包 5xx 仍直传 err.Error()**
- aiapi.go:57、:90：store/SQLite 内部错误原文返回给 AI token 持有者。writeInternalErr 纪律应扩展到 aiapi 包（本地加同款 helper 即可）。

**N4【低｜既定 defer】上游错误体 200 字符透传给所有登录用户**
- aichat.go:372 `LLM 返回 HTTP %d: %s`（truncate 200）→ aichat_api.go:189（502）与 :289（SSE error 事件）。A4 只列了 test/models 端点，chat 路径同病，建议 A4 落地时一并截断。

**N5【低】400 路径可能透出 SQLite 原始错误**
- CreateEntry/SaveTemplate 的 `s.DB.Exec` 错误原样 `return err`（store.go:135-137 等）→ api.go:311/349/399、admin.go:36/69 以 400 err.Error() 回显。信息量小（无凭据），但与 5xx 收敛纪律不一致，归 C1 顺带处理。

**N6【提示】GET /ai-config 对所有登录用户开放**
- 返回 base_url/model/协议/Key 尾 4 位（aichat_api.go:56-73）。base_url 可视为轻微敏感（暴露内部 LLM 拓扑）；单管理员内网部署可接受，若开放多用户建议收紧为 admin-only 或脱字段。

**N7【提示】settings 明文值恰以 `enc:v1:` 开头会使轮换永久中止**
- store.go:392-407：LIKE 命中 → Decrypt 失败 → 轮换整体报错。fail-closed（不会损坏数据），管理员改掉该 settings 值即可恢复；A2 的"非法密文不透传"落地后可一并消除。

**N8【提示】遮蔽 `***` 哨兵的固有碰撞**
- 真实敏感值恰为 `***` 时：导出→导入闭环被拒（keyhive.go:32，fail-safe 方向可接受）、更新时会被当哨兵保留旧值（store.go:155-160，固有语义）。概率极低，文档提示即可。另：ListEntries 坏行跳过策略使 export 会静默跳过损坏条目（备份少数据，有服务端日志），备份场景建议核日志。

## 遗漏与建议

1. **A1（轮换进程级写互斥）建议提前**：本轮轮换已扩到 settings，预读-事务窗口的影响面变大——轮换预读后、事务提交前若有新保存的 LLM Key（旧钥加密），会被事务内 UPDATE 用预读旧密文覆盖回旧值（丢失更新）。单管理员场景概率低，但修复成本小（RWMutex），建议随 N1/N2 一起进下一批。
2. **A2 的子项 ②（Decrypt 非法密文不透传）建议单独提前**：改动小、收益直接——同时关闭 key_check 构造绕过与 N7 的轮换中止面；argon2id 派生（子项 ①）维持原排期合理。
3. **N2 属"应本轮修而未覆盖"**：一行挂载位置调整，建议立即补，使"全站 body 限制"名副其实。
4. A3（会话绝对寿命/清理/Secure cookie）维持 v1.4.0 排期合理；A4 落地时把 chat 路径 200 字符回显（N4）一起收敛；G4（登录防爆破）维持触发式（内网单管理员），**暴露公网前为硬性前置**。
5. 测试盲区呼应：api 层缺 anthropic+空 base_url 端到端用例（N1 因此漏网）、aiapi 缺 body 超限用例——补修时各带锚点。

## 总体结论

**修复可信度评级：B+（可信，附条件）**

- 21 项中安全相关 17 项：**14 ✅ / 2 ⚠️ / 1 ❌**。P0（ai-config 越权外带）修复真实有效且测试锚定扎实；审计注入、遮蔽导出、轮换 settings、空密钥拒绝、加密一致性等核心项均验证到位；全量测试复跑 10 包全绿、build/vet 干净。
- 扣分点：**#12 在生产路径被 api 层预填抵消（N1，凭据错发仍可达）**；MaxBytesReader "全站"声明与事实不符（N2，aiapi 子树裸奔）；writeInternalErr 纪律未覆盖 aiapi 包（N3）。
- 剩余风险清单（按优先级）：N1 凭据错发（中）→ N2 token 面 DoS（中低）→ N3/N5 内部错误泄露（低）→ N4 上游回显（低，defer）→ N6/N7/N8（提示级）。
- 建议：补 N1/N2/N3 三个小修（合计约半天，各带测试锚点）后，本轮可视为安全收敛完成；A1 与 A2② 随下一批提前，其余按 03-refactor-plans.md 排期推进。
