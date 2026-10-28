# 第一轮角色复审（Go 后端 · 正确性/并发/代码质量）

- 复审人：资深 Go 工程师（正确性/并发方向）
- 日期：2026-10-28
- 范围：工作区未提交改动（`git diff`）中 `internal/*` 与 `cmd/keyhive/main.go`，对照 `docs/review/02-test-report.md` 第 3 节 21 项修复
- 结论速览：**19 项 ✅ / 2 项 ⚠️ / 1 项 ❌**（#12 端点分派在 API 层被架空），独立跑测全绿。详见下文与「总体结论」。

---

## 修复实现核验（逐项）

### #1 ai-config/test、models 移入 RequireAdmin —— ✅

- `internal/api/api.go:138`（`ar.Use(auth.RequireAdmin...)`）、`:155-156` 两个端点挂 admin 组。普通用户组（`api.go:127-132`）不再含外联端点。
- 凭据外带链路（服务端解密已保存 Key → 发往用户自填 URL）现在仅 admin 可触发。测试 `TestBug_AIConfigTestRequiresAdmin` + 13 端点非 admin 全 403 矩阵覆盖。无遗漏调用点。

### #2 全局 10MB MaxBytesReader —— ✅（有两个小缺口，见建议 P2/P3）

- `internal/api/api.go:24-37`：`limitBody` 先判 `r.ContentLength > maxBodyBytes` 直接 413（**chunked 请求 ContentLength=-1，正确落到 MaxBytesReader 分支**，不会误杀），`http.NoBody` 判空正确。
- `api.go:69-77` `readBody` 用 `errors.As(err, &maxErr)` 匹配 `*http.MaxBytesError` 映射 413——判定方式正确（Decode 会原样透传底层读错误，errors.As 兼容包装）。
- `api.go:101` `r.Use(limitBody)` 在 Routes 顶部，未登录端点（/auth/login）也受限 ✓。
- 缺口 1：`/api/v1/ai/*` 在 `cmd/keyhive/main.go:124-138` 独立挂载，**不经 limitBody**；`internal/aiapi/aiapi.go:109` 的 Reveal POST 直接 `json.NewDecoder(r.Body).Decode` 无上限。token 鉴权（reveal scope）先行，风险降级，但与「全站上限」的声明不符。
- 缺口 2：两处 handler 未走 `writeReadErr`——`api.go:389`（saveAsTemplate 把 readBody 错误吞掉、超限请求返回 201）、`webui.go:30-31`（genTOTP 忽略解码错误）。内存仍受 MaxBytesReader 保护，只是状态码语义丢失（400/201 而非 413）。
- chunked 超限→413 的路径无测试（现有 `TestBug_MaxBytesRejected` 走 Content-Length 预判分支），实现本身正确。

### #3 审计 detail 统一 audit.Detail() —— ✅

- `internal/audit/audit.go:11-17` json.Marshal 构造，Marshal 失败回退 `"{}"`（fail-safe）。diff 中 18+ 处调用点全部改造，含 users.go 的 `boolPtrJSON`/`boolJSON` 残留删除（`joinScopes` 已无调用方，建议顺手清掉，见 P3）。
- `TestBug_AuditDetailValidJSON` 双视角（直查表 + API 读）断言合法 JSON。标题含 `"` 与 `\` 的破坏字符覆盖到位。

### #4 VerifyPassword 畸形 PHC 串显式校验 —— ✅

- `internal/auth/password.go:59-63`：`t==0 || p==0 || m==0 || len(salt)==0 || len(want)==0` 前置拒绝。
- **panic 完备性已对依赖源码核实**：本仓库锁定的 `golang.org/x/crypto v0.57.0`（go.mod:8）中 `argon2.deriveKey` 仅剩 `time<1`、`threads<1` 两个 panic 点（memory 不足被库内 clamp、keyLen 无 panic；见模块源码 argon2.go:105-110），守卫已全覆盖。若将来升级/降级 x/crypto 也不会回退（m==0/salt/want 的检查额外覆盖了旧版 panic 与恒等比较两类风险）。`p` 按 uint8 解析，越界值被 Sscanf 拒绝 ✓。
- `m` 巨值（如 2^31）仍是 CPU/内存放大输入，但 encoded 只来自库内 users 表（写入侧参数固定），无网络注入面——不构成缺陷。

### #5 5xx 不回显内部错误 —— ✅（aiapi 有残留）

- `api.go:51-55` `writeInternalErr`（日志+通用文案），api 包 22 处改造到位。剩余 500 全是固定文案（"哈希失败"等）。
- 残留：`internal/aiapi/aiapi.go:57、90` 仍 `writeErr(w, 500, err.Error())`，store 错误可能带 SQL/路径细节给 token 持有者（低敏，但同类问题应一并收敛）。rotate.go 的三处带 err.Error() 是 admin 运维语义（含回滚/落盘指引），属有意保留，合理。

### #6 /export?masked=1 服务端遮蔽 —— ✅

- `api.go:160-176` 服务端 `MaskSecrets()`；CLI `admincli.go:124-130` 传 `?masked=1` 且保留客户端遮蔽兜底（对已遮蔽的 `***` 幂等）。审计 detail 记录 masked 标志 ✓。`TestExportMaskedParam` 断言整包响应不含明文 + 结构不变。

### #7 空 master.key 拒绝启动 —— ✅

- `internal/crypto/crypto.go:139-142`：TrimSpace 为空即拒绝，错误文案含修复指引。测试覆盖 0 字节与纯空白，且 `t.Setenv` 排除了 env 密钥干扰。

### #8 去掉 encryptFieldsWith 的 IsEncrypted 跳过 —— ✅

- `internal/store/store.go:48-62`。逐一核对全部写入方，**无双重加密路径**：
  - CreateEntry（api.go:306 / 导入 / 复制）：入参一律用户明文；
  - UpdateEntry（store.go:152-159）：`***` 哨兵先经 GetEntry 旧值还原为明文再加密 ✓；
  - Rotate（store.go:376-384）：先 decryptFields 解回明文再新钥加密 ✓；
  - 前端任何路径拿不到密文（list/get 均先解密再遮蔽），不存在回传密文被二次加密的场景。
- 语义正确的关键在注释里写清了（store.go:44-47）：写入侧一律是"待加密明文"。复制路径若以遮蔽视图为源会存入字面 `***`，属既有哨兵语义（create 不还原哨兵），非本轮回归；`TestEntryCopySemantics` 锚定了明文复制行为。

### #9 ListEntries 坏行隔离 —— ✅ 实现 / ⚠️ 启发式边界

- **无密文泄漏**：`scanEntry`（store.go:94-98）解密失败时返回的部分条目 `Fields=nil`（`decryptFields` store.go:65-81 出错一律返回 nil，部分解密的中间切片不外泄），ListEntries 只取 `e.ID` 记日志，坏行不进结果 ✓。
- 判定逻辑（store.go:211-237）：单条坏行跳过+日志；`rows.Err()` 仍严格；命中的行全坏 → 整体报错。部分损坏 vs 全库损坏的区分是**基于"命中行"的启发式**，两个边界：
  1. 窄搜索（q 恰好只命中那条坏行）会误报"疑似主密钥不匹配"并 500——行为安全（fail closed）但文案误导；
  2. **导出路径静默丢弃坏行**：`/export`（明文/遮蔽）经 ListEntries 拿到的是缺失坏行的清单，仅服务端日志留痕。对"导出后即删源"的迁移习惯（importer.go:55-57 注释自述）存在无声数据丢失风险，建议导出时对 bad>0 显式暴露（P2）。
- GetEntry 保持严格报错 ✓（更新坏行会失败而非误写，正确）。

### #10 轮换连同 settings 重加密（同事务） —— ✅ 事务边界 / ⚠️ 并发窗口

- 顺序正确：旧钥有效窗口内预计算（条目 store.go:369-385 + settings :386-419）→ 单事务内先条目、再 settings、最后 key_check（:427-451）→ Commit 后才换内存密钥（:453-456）。任一步失败 `tx.Rollback()`，内存密钥未动、key 文件未写，旧钥持续有效 ✓。`key_check` 显式排除避免被旧值覆盖 ✓。
- **并发窗口（已知遗留）**：读阶段（步骤 1-2.5）在事务外。MaxOpenConns(1) 只串行化语句，不串行化逻辑上的读-改-写：轮换期间并发 CreateEntry/UpdateEntry 可在预计算之后、Commit 之前用旧钥加密落库——提交换钥后该条目永久不可解密（数据丢失，非仅报错）。两次并发 rotate 同类。02-test-report §5 已自认（"进程级写锁进重构方案"），本轮无恶化，但**建议至少给 RotateAllEntries 与条目写路径加进程级互斥**（P1 建议，工作量小），不必等重构。
- 极端边角：settings 中**明文**恰好以 `enc:v1:` 开头（base_url/model 是用户可设明文）会被 rotate 尝试解密而整体失败——fail-closed 不丢数据，仅轮换被阻，P3 提示即可。

### #11 Anthropic 流 error 事件传播 —— ✅

- `internal/aichat/anthropic.go:281-296`：`error` 事件→错误返回，message 带 type 前缀，空 error 对象有兜底文案。`TestBug_AnthropicStreamErrorPropagated` 覆盖。

### #12 默认端点按协议分派 —— ❌ 包层修对了，API 层被架空（本轮最重要发现）

- 包层修复本身正确：`aichat.go:48-53` `defaultBaseURL()`，Chat/ChatStream/ListModels/TestConnection 四处入口 + anthropic 内部两处常量化，grep 无遗漏调用点；`endpoint_bug_test.go` 用拒绝代理观测外发 host，设计巧妙且有显式 baseURL 对照组。
- **但生产主链路永远走不到这个分派**：`internal/api/aichat_api.go:27` `currentLLMConfig()` 用 `orDefault(setting, aichat.DefaultBaseURL)` **协议盲**地预填 OpenAI 端点——protocol=anthropic 且 base_url 未设置时，`handleAIChat`（:152）、`handleAIChatStream`（:239）拿到的 cfg.BaseURL 恒非空（bigmodel），aichat 包内 `cfg.BaseURL == ""` 的分派永不触发，**x-api-key 仍发往 open.bigmodel.cn**。`handleAIConfigTest`（:366）、`handleAIListModels`（:396）的 `orDefault(req.BaseURL, saved.BaseURL)` 同样回退到预填值。
- 且会**固化为持久错误**：GET /ai-config（:67）同样协议盲返回 bigmodel 默认值 → 前端 `loadAI`（Settings.vue:262-263）把该值灌进表单 → `saveAI` 原样 PUT → base_url 被持久化为 bigmodel，此后连包层修复的触发条件都消失。
- 缺陷定级与原 P1 同级（anthropic 付费凭据发往异厂商端点）。测试只覆盖 aichat 包边界，无 API 层回归测试（grep 全仓 `currentLLMConfig` 零测试引用）——这正是"修在了测试够得着的那一层"的典型。

### #13 adminFlags 解析失败退出 2 —— ✅

- `admincli.go:94-104` 返回错误；cmdRotateKey（:309-315）打印用法+退出 2；cmdExport/cmdImport/cmdEdit 同步收口。`TestBug_AdminFlagParseErrorStops` 断言 reqCount==0（零请求），断言强度足够。

### #14 import 会话复用 —— ✅

- `admincli.go:43-69` adminSession（client+base 复用），cmdImport/importKeyhive 循环内只 `sess.call`。会话 TTL 7 天滑动续期（auth/store.go:16、92-93），**中途过期实际不可能**（导入分钟级 + 每次调用都续期）。中途 401（如管理员删会话）会让剩余条目逐条失败并打印服务端错误——可接受；可选优化：401 时重登一次（P3）。

### #15 Bitwarden notes 按敏感导入 —— ✅

- `importer.go:112-119`：is_secret=true；ai_visible 仅在 notes 为空时为 true。拒绝矩阵测试齐。

### #16 CSV LazyQuotes=false —— ✅

- `importer.go:57-70`；未闭合引号报错。edge 测试覆盖 BOM/CRLF/重复列名/带引号字段，畸形用例断言"报错而非脏数据"。

### #17 MCP 错误结果 NewToolResultError —— ✅

- `mcp.go:130-140` 两分支均 IsError=true。

### #18 config.json chmod 0600 —— ✅

- `login.go:117-122`：写后显式 chmod（os.WriteFile 的 perm 不管既有文件，注释准确）；Windows 失败忽略（仅只读位）正确。

### #19 服务器超时 + ListenAndServe —— ✅

- `main.go:68-72`：`srv.Addr = addr` 后用 `srv.ListenAndServe()`，修掉了 `http.ListenAndServe` 另建零配置 server 的经典错误 ✓。`main.go:146-150` ReadHeaderTimeout 10s / IdleTimeout 120s，不设 WriteTimeout 保 SSE——取舍正确（注释已说明）。慢速**请求体**仍无时间上限（仅 10MB 容量上限），如需进一步加固可评估 ReadTimeout 与 SSE 的相容性（P3）。

### #20 交付物（.dockerignore/CI/Dockerfile）—— 本角色不评（SRE 角色覆盖）

### #21 前端（改密反馈/搜索竞态）—— 本角色不评（前端角色覆盖）

---

## 测试质量抽查

抽查了 store_bug_test / endpoint_bug_test / api_security_test / auth_edge_test / cli/bug_test / crypto_bug_test / importer bug+edge / api_export_test / store_edge（复制语义）共 9 个文件：

- **断言的是正确行为**：每个 TestBug_ 文件头明示"断言正确行为，禁止弱化断言或删除"；失败信息描述的是期望语义而非实现细节。store 的前缀陷阱用例额外校验"落库值≠明文"（防读取侧兜底假绿）；auth 用 verifySafe 捕获 panic 双断言（不 panic 且不通过）；cli 用 reqCount==0 做零副作用断言。**未发现为凑绿弱化断言的痕迹**。
- 有对照组意识：`TestExplicitBaseURLUntouched`（显式 URL 不被改写）、好条目明文 round-trip、password 字段敏感对照等。
- **盲区（与残留缺陷直接相关）**：
  1. 无任何测试触碰 `currentLLMConfig`/handleAIChat 的端点分派集成路径——#12 的 API 层缺陷因此漏网；
  2. chunked 请求体超限→MaxBytesError→413 分支无覆盖（实现已核对正确）；
  3. 轮换与并发写的竞态无覆盖（自报进重构方案）。

## 独立跑测结果

- `go build ./...`：通过，退出码 0
- `go vet ./...`：通过，退出码 0
- `go test ./... -count=1`：**17 包全 ok，退出码 0**（cmd/keyhive、aiapi、aichat、api、audit、auth、cli、crypto、db、importer、kdbtest、mcpserver、model、store、templates、totp、version；web 无测试文件）
- 与 02-test-report 第 4 节的自报结论一致，无选择性跑测痕迹。

## 建议修复清单（分级）

**P1（合入前必须）**
1. #12 收尾：`currentLLMConfig`（aichat_api.go:27）、GET /ai-config（:67）、test/models 的回退默认值全部改为协议感知（空 base_url 时按 `cfg.defaultBaseURL()` 语义分派，或干脆不预填、留给 aichat 包分派）；并补一条**API 层**回归测试（anthropic + 未设置 base_url，经 handleAIChat/handleAIConfigTest 断言外发 host）。
2. 轮换写竞态最小修复：Store 增加操作级互斥（RotateAllEntries 与 CreateEntry/UpdateEntry/SaveTemplate 写路径共用），不必等 03-refactor-plans 的整体方案。

**P2（近期）**
3. `/api/v1/ai` 挂载纳入 limitBody（最简单：把 `r.Use(limitBody)` 提升到 main.go 根 router，chi 中间件会作用于全部子路由）。
4. 导出路径对坏行显式暴露：`/export` 在 bad>0 时于响应 meta（或响应头）标注丢弃条数，或导出直接 fail-closed，杜绝"导出后删源"的无声丢失。
5. `aiapi.go:57、90` 改用通用 500 文案（对齐 #5）。

**P3（择机）**
6. `api.go:389`（saveAsTemplate）、`webui.go:30`（genTOTP）改走 `writeReadErr`，413 语义一致。
7. ListEntries 全坏报错文案区分"窄搜索命中坏行"与"全库损坏"（如附带命中数与总数），避免误导排障。
8. 删除无调用方的 `joinScopes`（admin.go:172 附近）；补 chunked 413 用例；adminSession 401 重登一次；评估 ReadTimeout 对慢速体的加固。

## 总体结论

**有条件可合入（B+ / conditional approve）。** 21 项修复中 19 项实现正确、事务边界与错误映射质量高，新增测试断言的是正确行为且无弱化痕迹，独立构建/静态检查/全量测试全绿。但存在一个实质未愈的 P1：**#12 协议分派修复在生产调用链上被 `currentLLMConfig` 的协议盲默认值架空，anthropic 凭据仍会发往智谱端点，且会被前端回显持久化**——该问题恰好落在测试覆盖的盲区，建议作为合入阻塞项先修（改动面小：1 个函数 + 1 处 GET 默认 + 1 条 API 层测试）。轮换并发写窗口属已知遗留（自报进重构方案），建议按 P1 建议以最小互斥提前兜底；其余为一致性与可观测性打磨。
