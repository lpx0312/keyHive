# mcpserver 模块（internal/mcpserver）

## 职责

把 keyHive 客户端能力以 stdio MCP server 暴露（`keyhive mcp` 子命令），供 ZCode / Claude / Cursor 等 MCP 客户端接入。注册 kh_status / kh_list / kh_search / kh_get / kh_reveal 五个工具；实现完全复用 `internal/cli` 的调用函数与 `~/.keyhive/config.json` 配置，因此与 CLI 走完全相同的服务端路径——同样的遮蔽规则、同样的 reveal 审计，没有任何旁路。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/mcpserver/mcp.go` | `Run()`：server 组装、五工具注册与描述、stdio 循环、`callAndWrap` / `requireRead` / `text` 辅助 |
| `internal/mcpserver/mcp_test.go` | 五工具主路径与配置缺失分支 |
| `internal/mcpserver/edge_test.go` | 畸形 JSONRPC、不存在条目、stdio EOF 优雅退出 |
| `internal/mcpserver/bug_test.go` | 错误结果必须带 IsError 的回归 |

## 对外接口

- `Run() int` — 启动 stdio MCP server，返回进程退出码；`server.ServeStdio` 失败打印错误并返回 1。server 元信息：
  - `server.NewMCPServer("keyhive", version.Version, server.WithToolCapabilities(false), server.WithInstructions(...))`；
  - Instructions 内置遮蔽优先纪律：「先用遮蔽信息（字段名+description 注释）判断，能不取明文就不取；明文只注入执行环境，绝不写入文件/git/对话正文；每次 reveal 均记审计」（常量 `discipline`）。

五工具（均复用 `internal/cli` 导出函数）：

| 工具 | 参数 | 后端调用 | 语义 |
|---|---|---|---|
| kh_status | — | `cli.LoadConfig` + `cli.ListEntries(cfg, "")` | 检查服务连通性、配置文件与令牌状态；取密钥失败时先用它排查 |
| kh_list | category（可选） | `cli.ListEntries` | 列出 AI 可见条目；敏感值遮蔽 `***`、注释完整 |
| kh_search | query（必填） | `cli.SearchEntries` | 关键词搜索遮蔽结果与注释；任务需要凭据时先查库而不是问用户要 |
| kh_get | id（必填） | `cli.GetEntry` | 单条遮蔽详情；id 来自 kh_list/kh_search 结果 |
| kh_reveal | id、field（必填） | `cli.RevealField` | 取单敏感字段明文（需 token_reveal；每次服务端记审计） |

- 结果包装：`callAndWrap(call)` 把 `(data, code, err)` 统一转 `*mcp.CallToolResult`；`text()` = `mcp.NewToolResultText`。
- 参数与配置校验：`req.RequireString` 缺失时返回文本错误结果（不 panic）；`requireRead()` 检查 `token_read` 已配置，否则返回带 Web UI 指引的错误文本；kh_reveal 单独检查 `token_reveal`。

客户端接入形态（与 `skills/keyhive/SKILL.md`、`.claude-plugin` 呼应）：

- 启动命令 `keyhive mcp`，stdio 传输（标准 JSON-RPC over stdin/stdout，错误/日志走 stderr）；
- 工具发现由 MCP 客户端自动拉取，无需手工注册；
- kh_status 顺带回显 base_url 与配置路径，客户端可据此判断连的是哪个实例。

## 依赖关系

- 依赖：`github.com/mark3labs/mcp-go`（mcp + server）、`internal/cli`（`LoadConfig` 与四个调用函数）、`internal/version`。
- 被依赖：`cmd/keyhive.dispatch` 以 `mcp` 子命令拉起（`keyhive mcp`）。
- 运行时依赖 config.json 中的 AI 令牌与远端 keyHive 服务；
  - `KEYHIVE_CONFIG` / `KEYHIVE_BASE_URL` 环境变量经 `cli.LoadConfig` 同样生效；
  - 服务不可达时经 `ErrServiceDown` 转为带启动指引的错误结果。

## 安全与不变量

1. **错误结果必须带 IsError**：`callAndWrap` 的错误分支用 `mcp.NewToolResultError`（isError=true），HTTP 非 200 同样走错误结果——MCP 客户端（ZCode/Claude 等）靠该标志区分工具成败，若以纯文本返回会被当作正常内容进模型上下文（`TestBug_MCPErrorFlagged`）。
2. **与 CLI 同配置同审计**：不自带任何凭据存储，读同一 `cli.ConfigPath()`；reveal 走 `POST /api/v1/ai/entries/{id}/reveal`，审计在服务端落库，MCP 侧无法绕过或静默。
3. **遮蔽优先的使用纪律双重注入**：server Instructions 与 kh_reveal 的 description 均带 `discipline` 文案；kh_search 描述明确“任务需要任何凭据时先用它查库里有没有，而不是问用户要”，kh_reveal 要求“返回值不要写入文件/git/对话正文”。
4. **可见性与服务端一致**：`ai_visible=false` 的条目在服务端已按 404/过滤处理，MCP 层无额外可见性；遮蔽由服务端完成，本包不做二次解析。
5. 缺 query/id/field 参数返回文本错误而非工具崩溃；token 未配置时返回可操作的指引文案（去 Web UI「AI 令牌」页创建并填入配置路径）。
6. stdio 生命周期：stdin EOF 即优雅退出（`TestRunServeStdioEOF`）；畸形 JSONRPC 消息不击垮 server。

## 测试锚点

`TestBug_MCPErrorFlagged`、`TestMCPTools`、`TestMCPConfigErrors`、`TestCallAndWrap`、`TestMCPGetNonexistentEntry`、`TestMCPMalformedJSONRPC`、`TestRunServeStdioEOF`。
