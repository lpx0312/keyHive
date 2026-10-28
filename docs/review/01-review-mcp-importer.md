# MCP 与导入（mcpserver/importer/kdbtest）代码审查报告

> 审查对象：`internal/mcpserver/`（mcp.go、mcp_test.go）、`internal/importer/`（importer.go、keyhive.go、importer_test.go、keyhive_test.go）、`internal/kdbtest/`（fixture.go、fixture_test.go），共 3 包 8 文件，全部通读。
> 审查方式：只读。为核实行为交叉查阅了 `internal/cli/cli.go`、`internal/cli/admincli.go`、`internal/aiapi/aiapi.go`、`internal/api/api.go`、`internal/api/webui.go`、`internal/store/store.go`、`internal/model/model.go`、`internal/totp/totp.go`、`internal/db/db.go`、`internal/templates/templates.go`、`cmd/keyhive/main.go`，以及依赖库 mcp-go v1.1.1 的 `server/stdio.go` 源码（module cache）；另用独立 Go 探针实测了 `encoding/csv` 在与 parseCSV 相同配置（FieldsPerRecord=-1 + LazyQuotes=true）下对畸形输入的真实行为。三包 `go test` 实测全绿。

## 概览

范围共 8 个文件、约 1000 行。总体质量良好：MCP 层完全不自带遮蔽逻辑，kh_list/kh_search/kh_get 的遮蔽由服务端 aiapi 的 MaskSecrets 单一职责承担，与 AI API 标准结构性一致；kh_reveal 是唯一明文出口且逐次记审计；keyhive 恢复导入在解析层整体拒绝遮蔽版、清零元数据后统一走 CreateEntry 重新加密；dry-run 与真实导入共用同一次解析，预览不会骗人；kdbtest 夹具（内存库 + 单连接 + 随机主密钥 + t.Cleanup）无端口与文件系统共享，天然并行安全。

主要问题一处 P1：CSV 导入把 Bitwarden notes 按非敏感字段落地且强制 ai_visible=true，安全笔记（恢复码等）明文直达 AI 上下文，绕过遮蔽与 reveal 审计两级控制；一处 P2：LazyQuotes 容忍未闭合引号，实测会静默吞掉后续所有行、产出污染的密码值且全程无错误。其余为 P3 级：MCP 错误语义（isError 缺失、SIGTERM 退出码、kh_status 误导性输出）与导入器的若干静默失败模式。另注：CLI import 循环每条重新登录的问题已在 `01-review-cli.md` 以 P1 报告（admincli.go 不在本范围），本文不重复计。

发现计数：P0 × 0，P1 × 1，P2 × 1，P3 × 8。

## 发现清单

### P1（严重）

**[P1] CSV 导入把 notes 映射为非敏感字段且强制 ai_visible=true，Bitwarden 安全笔记明文直达 AI 上下文**（importer.go:110、importer.go:115；Web 同路径 webui.go:105）

`ToEntryJSON` 将 notes 以 `add("notes", "备注", e.Notes, false, true)` 导入——is_secret=false（importer.go:110），且条目固定 `"ai_visible": true`（importer.go:115），CLI 与 Web 导入共用该映射。Bitwarden 的 notes 列是自由文本，用户习惯存放恢复码、密保问题答案、银行卡号/私钥片段（Bitwarden 自家的"安全笔记"本就是加密存储的条目类型）。导入后：

- kh_list/kh_search/kh_get 对非敏感字段不做遮蔽（aiapi.go:65/98/77 仅对 IsSecret 字段调 MaskSecrets，见 model.go:81-87）——所有 notes 明文批量进入任何持有 read 令牌的 MCP/AI 客户端上下文；
- 不经过 kh_reveal 的逐字段取用与 field_reveal 审计，产品在 MCP instructions 里强调的"先用遮蔽信息判断、能不取明文就不取"纪律（mcp.go:18）被导入器一次性击穿；
- 与产品自身口径形成反差：内置 web_account 模板（templates.go:344-352）只有窄口径的非敏感 remark（"账号用途备注"），并不设自由 notes 字段。

且没有任何开关让用户选择把 notes 按敏感导入，CLI/Web 的导入输出（标题预览、ok/fail 计数）也无相关警告。

影响：从 Bitwarden/Chrome 迁移几百条时，所有 notes 内容（含高概率敏感的恢复码类信息）静默变为 AI 明文可见，遮蔽与审计两级控制同时失效。

修复建议：导入时 notes 默认 is_secret=true（description 注明"从 Bitwarden 导入的备注，可能含恢复码等敏感信息"），或提供 `--notes-secret` / `--no-ai-visible` 开关；至少在导入预览与结果输出中明确提示"notes 将以明文对 AI 可见（共 N 条）"。

### P2（一般偏严重）

**[P2] LazyQuotes 容忍未闭合引号，畸形/截断 CSV 静默吞掉后续行、产出污染数据**（importer.go:57-64）

`parseCSV` 设置 `r.LazyQuotes = true`。实测（Go 1.26 encoding/csv，与 parseCSV 完全相同配置）：输入

```csv
name,login_username,login_password
alice,"pw
bob,bobpw
```

解析结果为 header 正常 + 单行 `["alice" "pw\nbob,bobpw\n"]`——未闭合引号把其后**所有行**吞进 alice 的密码字段，bob 整条静默丢失，全程无任何错误。文件被截断（传输中断）、手工编辑引号失误都会触发：导入"成功完成"，密码是脏值（含换行与后续行内容），直到登录失败才被发现；条目数少于预期也没有提示（解析器无错误，计数照常打印）。

影响：密码管理器的导入是"迁移后即删源"的高信任操作，静默脏数据意味着用户可能在删除 Bitwarden 库之后才发现密码损坏。

修复建议：`LazyQuotes = false`（Bitwarden/Chrome 官方导出是规范的 RFC4180 CSV，严格模式只会拒绝真正畸形的文件，把 ErrQuote 显式暴露给用户）；同时解析结果为 0 条时不要静默成功，应报"未解析到任何记录（表头与所选格式不匹配？）"。

### P3（建议）

**[P3] kh_status 未按承诺展示令牌配置状态，token 未配置时输出误导性"令牌无效"**（mcp.go:59-65）

`toolStatus` 不走 `requireRead`，直接以空 `TokenRead` 发起请求（`Authorization: Bearer ` 空值）→ 服务端 401 → 返回 `HTTP 401: {"error":"令牌无效、已吊销或已过期"}`。而工具描述承诺"检查 keyHive 服务连通性、配置文件与令牌配置状态"（mcp.go:28）。对比 CLI 的 cmdStatus（cli.go:452-453）会先打印 token_read/token_reveal"已配置/未配置"。现状下 AI 排障时会把"未配置"误判为"令牌被吊销"，且得不到"去哪里配"的指引。建议：镜像 CLI cmdStatus 先报告配置状态，token 未配置时不再发起请求。

**[P3] 工具错误均以正常文本返回，MCP 客户端无法区分成败**（mcp.go:130-139）

`callAndWrap` 对 err 与非 200 分支一律 `mcp.NewToolResultText`，isError 标志缺失。mcp-go v1.1.1 提供 `NewToolResultError`（isError=true，见库内 typed_tools.go）。MCP 客户端（ZCode/Claude 等）依赖 isError 判断工具是否失败，`HTTP 401: ...` 之类的文本会被当作正常返回值进入模型上下文。建议错误分支改用 `mcp.NewToolResultError`（参数校验类错误同理）。

**[P3] SIGTERM/SIGINT 正常关闭被当作错误退出（退出码 1 + stderr 噪音）**（mcp.go:52-56）

mcp-go v1.1.1 在信号触发 cancel 后 `Listen` 返回 `context.Canceled`（stdio.go processInputStream 循环首行 `ctx.Err()` 检查，已核验库源码），`Run` 将其打印"MCP server 错误: context canceled"并返回 1。MCP 客户端结束时给子进程发 SIGTERM 是常规操作，不应表现为故障。建议对 `context.Canceled`（及 `errors.Is` 变体）特判为正常退出返回 0。

**[P3] stdio 单行消息无大小上限（依赖库行为，记录在案）**（mcp.go:52；mcp-go v1.1.1 server/stdio.go readNextLine）

mcp-go 用 `bufio.Reader.ReadString('\n')` 读行，无长度上限——无换行的超长行（异常或恶意输入）会无界累积内存直至 OOM。畸形 JSON 本身的处理良好（核验库源码：回 PARSE_ERROR 响应且连接存活，EOF 干净返回）。本机 stdio 威胁模型下风险低；mcp-go v1.1.1 未暴露行长上限选项，升级依赖时关注。

**[P3] 表头/格式错配时静默产出空壳条目或 0 条记录**（importer.go:31-50、importer.go:74-79）

实测行为：Chrome CSV 按 bitwarden 解析 → cols 有 name 无 login_password → 每行产出"仅标题、零字段"的空壳条目并真实入库（Validate 不拒绝空 fields）；Bitwarden CSV 按 chrome 解析同理。文件首行为空白字符（非空行）时表头变成 `["   "]`，全部条目被过滤，结果"解析到 0 条记录"且无错误。dry-run 预览只显示 title/username，用户难以察觉字段全空。建议：解析前校验必需列名存在（bitwarden 需 login_password，chrome 需 password），0 条结果显式报错而非静默成功。

**[P3] 重复列名不检测，后者静默覆盖前者**（importer.go:74-79）

`cols[strings.TrimSpace(h)] = rec[i]` 在表头重名时后列覆盖前列（实测 `name,name` + `A,B` 时 map 取到 B）。恶意或畸形 CSV 可借此把值错位到敏感列。建议构建表头映射时检测重复列名并整体报错。

**[P3] 测试遗留死代码，掩盖了 401 流程端到端断言缺失**（mcp_test.go:169-180）

`TestMCPConfigErrors` 末尾定义 `setup2` 后仅 `_ = setup2`、`_ = json.Marshal`——为绕过未使用变量检查的死代码；注释"callAndWrap 分支直接单测"承认了真实 401 服务 → 错误文本透传的端到端断言从未执行（该分支仅由 `TestCallAndWrap` 以注入函数方式单元覆盖）。建议删除死代码，或把 setup2 真正用起来（起 401 服务，断言输出含 "HTTP 401"）。

**[P3] Fixture.SetPassword 不校验影响行数**（fixture.go:51-53）

`UPDATE users SET password_hash=? WHERE username=?` 对不存在的用户名静默更新 0 行、无错误；调用方拼错用户名时会在后续断言处以更困惑的方式失败。建议检查 `res.RowsAffected == 1`，否则 `t.Fatalf`。

### 专项核查结论（不计入发现级别）

1. **遮蔽一致性**：kh_list/kh_search/kh_get 的遮蔽完全由服务端 aiapi 的 MaskSecrets 承担，MCP 层只透传响应字节，不存在第二套遮蔽逻辑，与 aiapi 标准结构性一致；kh_reveal 输出的 value 是唯一明文出口，走 field_reveal 审计（aiapi.go:126）。
2. **错误信息泄密**：各错误路径（缺参数、token 未配置、HTTP 非 200、服务端错误 JSON）均不含令牌明文与敏感字段值，令牌只出现在 Authorization 头；未配置提示只给出配置文件路径。非 200 时响应体整段透传（含服务端 500 的内部错误文本），本机威胁模型下可接受。
3. **遮蔽版拒绝绕过**：ParseKeyhive（keyhive.go:32）为"任一 is_secret 字段值严格等于 `***` 即整体拒绝"——部分遮蔽、混合真值与 `***` 均被拒（命中第一个即返回），无法绕过；非敏感字段的字面 `***` 放行是设计行为（keyhive_test.go:41-43 锚定）。真值恰好为 `***` 的字段会被误拒，方向是 fail-safe，可接受。
4. **导入加密流程**：keyhive 恢复导入两条路径（CLI `AddEntry` → POST /entries → createEntry → Store.CreateEntry；Web 直调 Store.CreateEntry）都经 Validate + encryptFields **重新加密**后 INSERT，不存在明文直入库；ID/created_at/updated_at 在解析层清零（keyhive.go:36），无 ID 冲突——代价是重复导入同一文件会产生重复条目（无去重，恢复场景下合理，有审计可查）。
5. **dry-run 同路径**：CLI（admincli.go:158/210）与 Web（webui.go:81/121）的预览和真实导入共用同一次 Parse/ParseKeyhive 结果，预览不会骗人；差异仅在预览展示条数（CLI 5 条、Web 20 条）与 dry-run 不要求 admin 密码。
6. **公式注入（=cmd()）**：ToEntryJSON 未对 `=` `+` `-` `@` 前缀做中和，但当前全库导出仅 JSON、Web 前端为 Vue 默认转义插值、无 CSV 导出功能——攻击链不成立，暂无需处理；若未来增加 CSV 导出，需在导出侧对该类前缀中和（列入下节盲区跟踪）。

## 做得好的地方

1. **遮蔽职责单一**：MCP 层零遮蔽逻辑、完全复用 aiapi 调用函数（mcp.go:64/73/85/97 → cli.go → aiapi.go），结构性杜绝"MCP 与 API 两套遮蔽标准漂移"这一类事故。
2. **遮蔽版恢复导入的拒绝质量高**：报错指明条目名、字段名、原因与出路（"请用明文导出的 JSON"），且是整体拒绝而非部分导入（keyhive.go:33）；空文件/非数组/空数组/类型错误均有区分明确的错误信息。
3. **导入统一重加密 + 审计**：所有导入最终走 Store.CreateEntry（加密落库），元数据（ID/时间戳）不入库；CLI 每条 entry_create 审计、Web 汇总 import 审计。
4. **CSV 兼容性基础扎实**：UTF-8 BOM 剥离（importer.go:28）、CRLF、空行跳过、quoted 多行字段（notes 含换行）实测均正确；otpauth URI 的 secret 提取让导入的 TOTP 可直接被 `keyhive totp` 使用。
5. **kdbtest 夹具干净**：内存库 + `SetMaxOpenConns(1)` + `t.Cleanup(d.Close)` + 每次随机主密钥（fixture.go:20-42、db.go:27），无端口、无共享临时目录、无全局状态，天然并行安全。
6. **MCP 的 AI 使用纪律贯穿始终**：server instructions 与 kh_reveal 等工具 description 均内嵌"先遮蔽后 reveal、明文不入正文、reveal 记审计"的纪律文案（mcp.go:18/24/47）。

## 测试盲区

- ServeStdio 仅测了 stdin EOF 退出（mcp_test.go:205-223）；畸形 JSON-RPC（半截 JSON、超长行、错误的 jsonrpc 字段）与 SIGTERM 退出码行为无测试——均为 mcp-go 库行为，本项目无护栏测试兜底。
- mcp_test 的 aiHandler 是手写 mock，与真实 aiapi 的响应结构可能漂移（真实 API 改字段/遮蔽策略时，mock 测试仍然全绿）；未与 kdbtest 真实栈联动做一次端到端。
- importer 无畸形 CSV 用例：未闭合引号、前导空白行、重复列名、格式错配（chrome↔bitwarden 互喂）、0 条结果——现有测试全部是 happy path。
- ParseOTAuth 对无 secret 参数的 otpauth URI 返回空串 → totp_secret 字段被静默丢弃（add 跳过空值），无测试锚定该行为。
- keyhive 导入重复执行同一文件的去重/重复行为无测试（当前行为：产生重复条目）。
- kdbtest 的 `:memory:` 正确性隐式依赖 db.Open 的 `SetMaxOpenConns(1)`（db.go:27）；若未来放开连接池，内存库将裂成多个空库，夹具会静默错乱——无守护性断言。
- Web importCSV 请求体无大小上限（api.go:32-35 readBody 直接 Decode），超大 CSV 的内存行为无测试。
- 公式注入：当前攻击链不成立（无 CSV 导出），若增加导出功能需补前缀中和与对应测试（见专项核查第 6 条）。
