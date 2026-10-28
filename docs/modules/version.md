# 版本信息（internal/version）

## 职责

提供全仓唯一的版本号来源：`Version`/`Commit` 两个包级变量由构建时 ldflags 注入
（形如 `-X keyhive/internal/version.Version=v1.2.0`），`String()` 把版本号、commit
短哈希与 Go 运行时版本拼成一行人类可读描述，供启动日志、版本接口与 MCP 状态上报
统一取用。包本身零内部依赖、零 IO，是最底层的基础件。

## 关键文件与入口

| 文件 | 关键函数/类型 | 说明 |
|---|---|---|
| version.go | `Version`、`Commit` | 包级 string 变量，默认 `"dev"` / `"none"`，构建时注入覆盖 |
| version.go | `String() string` | 拼接 `Version (Commit) runtime.Version()`，无 commit 时省略括号段 |

## 对外接口

- `Version string`
  - 语义化版本号；未注入 ldflags 的本地构建为 `"dev"`。
  - 不变量：默认值非空（`TestDefaults` 锚定），调用方可直接拼接无需判空。
- `Commit string`
  - 构建 commit 短哈希；`"none"` 与 `""` 均视为"无 commit"，`String()` 不拼接括号段。
- `String() string`
  - 契约形态一（有 commit）：`v1.3.12 (0ffd905) go1.26.4`（版本基线示例）。
  - 契约形态二（无 commit）：`dev go1.26.4`。
  - 调用方只需直接打印，拼接规则由本包内部保证；不保证内容可被安全用于
    HTML 上下文（见下节）。
- 三个消费点的具体形态：
  - `dispatch` 的 `version` 子命令：`fmt.Println(version.String())` 后返回 0；
  - `buildServer` 启动日志：`log.Printf("keyHive %s | 主密钥来源: %s", version.String(), keySrc.Desc)`；
  - `/api/v1/version` 接口返回 `{"version": version.Version}`（`writeJSON`），
    MCP 侧把 `version.Version` 传给 `server.NewMCPServer` 作为服务器版本——
    这两处只取裸版本号，不含 commit 与 runtime 信息。

## 依赖关系

- 依赖：仅标准库 `runtime`，无任何内部包依赖。
- 被依赖：`cmd/keyhive`（`dispatch` 的 `version` 子命令打印、`buildServer` 启动日志
  `keyHive %s | 主密钥来源: ...`）、`internal/api`（`/api/v1/version` 接口返回）、
  `internal/mcpserver`（MCP kh_status 工具上报）。
- 数据流一句话：构建管线 ldflags → 本包变量 → 三处消费点原样输出。

## 安全与不变量

- `Version`/`Commit` 可被 ldflags 注入任意字符串，`String()` 只做拼接不做转义；
  仓库内三处消费均为纯文本日志或 JSON 字符串字段，无注入面。若未来新增 HTML
  渲染调用点，转义责任在调用方。
- 不得引入任何内部依赖：本包处于依赖图最底层，任何包都应能引用它而不造成环。
- 默认值 `"dev"`/`"none"` 保证未走构建管线的 `go build` / `go test` 产物也能正常
  启动并输出可辨识的版本串。
- 包内不做并发保护，但两变量实际只在进程初始化（ldflags）时写入、之后只读，
  无数据竞争风险。

## 测试锚点

- `version_test.go` 的 `TestString` 锚定三种输出形态：
  - 默认（`Commit="none"`）：以 `dev ` 开头且不含 `(`；
  - 有 commit（`Commit="abc1234"`）：含 `(abc1234)`；
  - 空 commit（`Commit=""`）：不拼括号段。
  测试结束时恢复原 `Commit` 值，避免污染同包其他测试。
- `TestDefaults` 锚定 `Version` 默认值非空。
