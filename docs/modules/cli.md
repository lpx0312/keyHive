# cli 模块（internal/cli）

## 职责

`keyhive` 二进制的终端/脚本客户端：12 个子命令覆盖只读查询（AI 令牌走 /api/v1/ai）、admin 通道操作（export / import / rotate-key / add / edit，走人用 API）与本地配置管理。`internal/mcpserver` 直接复用本包的调用函数，二者共享同一份 `~/.keyhive/config.json` 与同一套服务端遮蔽/审计链路，没有任何独立后门。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/cli/cli.go` | `Config` / `ConfigPath` / `LoadConfig`、`Run` 子命令分发、只读命令（list/search/get/reveal/totp/status/add）、`apiCall` 与 `ErrServiceDown` |
| `internal/cli/admincli.go` | admin 通道：`adminClient` / `adminSession` / `loginAdmin` / `adminCall` / `adminFlags`、export / import / rotate-key / edit 实现 |
| `internal/cli/login.go` | `cmdLogin`：交互/非交互创建或合并配置，写前连通性校验，0600 落盘 |
| `internal/cli/cli_test.go`、`cli_edge_test.go`、`cli_run_test.go`、`admincli_test.go`、`bug_test.go` | 全子命令、错误分支与安全修复回归 |

## 对外接口

- `Run(args []string) int` — 入口，返回进程退出码（用法/参数错误 2，运行失败 1，成功 0）。子命令一览：
  - 只读（token_read）：`list [--category <c>] [--stale N]`、`search <关键词> [--stale N]`、`get <id>`、`status`；
  - 明文/动态码（token_reveal）：`reveal <id> <字段名>`、`totp <id> [--field <字段>]`；
  - 配置：`login [--url] [--token-read] [--token-reveal] [--no-verify]`；
  - admin 通道：`add --file <json> [--user] [--pass]`、`edit <id> <field>=<value>...`、`export [--masked]`、`import --file --format bitwarden|chrome|keyhive [--dry-run]`、`rotate-key`。
- `Config{BaseURL, TokenRead, TokenReveal}` — `~/.keyhive/config.json`；相关环境变量：

| 环境变量 | 作用 |
|---|---|
| `KEYHIVE_CONFIG` | 覆盖配置文件路径（默认 `~/.keyhive/config.json`） |
| `KEYHIVE_BASE_URL` | 临时覆盖服务地址，不改配置文件（CLI 与 MCP 同时生效，连另一台实例用） |
| `KEYHIVE_ADMIN_PASS` | admin 命令密码兜底，避免 `--pass` 进 shell 历史 |

  login 写入后显式 `os.Chmod 0600`（WriteFile 的 perm 不收紧已有文件）。
- 复用调用入口（MCP 亦调用，均经 `apiCall`：Bearer + `/api/v1/ai` 前缀、10s 超时、网络失败归一 `ErrServiceDown` 并附 docker 启动指引）：
  - `ListEntries(cfg, category)`、`SearchEntries(cfg, q)`、`GetEntry(cfg, id)`、`RevealField(cfg, id, field)`；
  - `AddEntry(cfg, user, pass, entryJSON)` — admin 登录换 cookie 后 POST /api/v1/entries（AI 令牌保持只读）。
- admin 会话：`loginAdmin(cfg, user, pass)` 登录一次返回 `adminSession`，`sess.call(method, path, body)` 复用 cookie 批量发请求；单条操作走 `adminCall`；`adminFlags(args)` 解析 `--user`（默认 admin）/`--pass`（`KEYHIVE_ADMIN_PASS` 兜底）。

退出码约定（脚本/CI 可依赖）：

| 退出码 | 含义 | 典型来源 |
|---|---|---|
| 0 | 成功 | — |
| 1 | 运行失败 | 服务不可达（`ErrServiceDown` + docker 启动指引）、HTTP 非 2xx、令牌未配置、部分导入失败 |
| 2 | 用法/参数错误 | 未知子命令、缺参数、flag 解析失败（含 adminFlags）、JSON 无效、敏感字段显式设 `***` |

## 依赖关系

- 依赖：`internal/importer`（CSV / keyhive JSON 解析）、`internal/totp`（`Current`/`ParseOTAuth` 本地算动态码）、`golang.org/x/term`（令牌输入遮蔽回显）。
- 被依赖：`cmd/keyhive.dispatch`（login/…/rotate-key/help 分支）、`internal/mcpserver`（五工具全部后端）。
- 运行时依赖远端 keyHive 服务与配置文件中的 AI 令牌；本包不持有密钥、不直接读写数据库。

## 安全与不变量

1. **adminFlags 解析失败必须退出码 2**：`rotate-key` / `edit` 打错 flag 时打印用法并退出，禁止带默认参数继续执行——否则会误触真实的主密钥轮换（`TestBug_AdminFlagParseErrorStops`）。
2. **import 单次登录**：`loginAdmin` 一次 argon2id 验证换一个会话，N 条导入复用同一 cookie（此前逐条登录使服务端开销与登录审计随条数翻倍，`TestBug_ImportSingleLogin`）；管理员密码只经 `--pass` 或环境变量 `KEYHIVE_ADMIN_PASS`，避免进 shell 历史。
3. **遮蔽导出双保险**：`export --masked` 请求 `/api/v1/export?masked=1` 由服务端遮蔽；若旧服务端忽略该参数，客户端再对 `is_secret` 字段补一遍 `value="***"` 兜底（已遮蔽的再遮蔽结果不变，`TestCmdExportMaskedBadResponse`）；明文导出必打“勿提交仓库、用完即删”警示，两种导出服务端均记 `export` 审计。
4. **reveal / totp 均记审计且输出警示**：`reveal` 打印后提示“已取明文并记录审计；不要写入文件/git/对话正文”；`totp` 三步流程 = 遮蔽详情定位字段（key 含 totp 或说明含 TOTP/两步验证）→ `RevealField` 取密钥（记审计）→ 本地 `totp.Current` 计算，密钥不落盘不回显。
5. **edit 的哨兵语义**：先 GET 遮蔽版条目，未提及的敏感字段以 `***` 回传由服务端保留原值（无需全量明文）；显式把敏感字段设为 `***` 被客户端拒绝（退出码 2）；只能改已存在字段与 title/category/description/ai_visible，新增字段引导去 Web 表单（需填给 AI 的注释）。
6. `login` 写前用 token_read 实际调一次 list 校验连通性（`--no-verify` 可跳过）；令牌输入终端遮蔽回显（`promptSecret`，管道/重定向退化为普通读）；非交互环境缺参直接退出 2 并提示所需 flag。
7. `--stale` 过滤纯客户端后处理：按 `updated_at` 早于 cutoff 保留（`filterStale` 用 `json.RawMessage` 保持字段不动）；无参数时默认提示超 90 天未更新的条数。

## 测试锚点

`TestBug_AdminFlagParseErrorStops`、`TestBug_ImportSingleLogin`、`TestCmdExportMaskedBadResponse`、`TestCmdEditFlow` / `TestCmdEditFullMatrix`、`TestRunAddExportImportRotate`、`TestRunRevealAndTOTP`、`TestRunStatusListSearchGet`、`TestRunLoginNonInteractive`、`TestServiceDownThroughCommands`、`TestConfigPathEnvOverride`、`TestLoadConfigBaseURLOverride`、`TestStaleFilterAndCount`。
