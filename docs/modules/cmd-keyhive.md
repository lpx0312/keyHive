# 命令入口与服务组装（cmd/keyhive）

## 职责

单个二进制三种形态的入口：`dispatch` 把无参/`serve` 分流到 HTTP 服务，把 12 个
客户端子命令交给 `internal/cli`，把 `mcp` 交给 `internal/mcpserver`；服务形态下由
`buildServer` 完成"主密钥 → 数据库 → key_check 校验 → store/api/aiapi → chi 路由 →
embed SPA"的全量组装，并集中配置 HTTP 超时与静态资源缓存策略。

## 关键文件与入口

| 文件 | 关键函数/类型 | 说明 |
|---|---|---|
| main.go | `main` | 调 `dispatch(os.Args[1:])`，返回 ≥0 则以该码退出，否则 `runServe` |
| main.go | `dispatch(args []string) int` | 子命令分发；返回 -1 表示继续启动服务；未知子命令 `log.Fatalf` |
| main.go | `runServe`（内部） | 读 `KEYHIVE_ADDR`/`KEYHIVE_DATA` 环境变量，调 `buildServer` 后 `srv.ListenAndServe()` |
| main.go | `buildServer(dataDir) (*http.Server, *sql.DB, error)`（内部） | 组装数据库+主密钥+全部路由；`runServe` 与测试共用；返回的 `*sql.DB` 由调用方关闭 |
| main.go | `ensureKeyCheck(db, cipher)`（内部） | `settings.key_check` 首启写入、非首启校验主密钥匹配 |
| main.go | `spaHandler(dist fs.FS) http.Handler`（内部） | SPA 静态资源：路由回退 + 分级缓存头 + gzip |
| main.go | `envOr(key, def)`（内部） | 环境变量取值，TrimSpace 后为空则用默认值 |

## 对外接口

包 `main` 无导出符号，契约体现在三个被测试锚定的内部函数：

- `dispatch` 的返回码语义：
  - `nil` / `"serve"` → -1（继续 serve）；
  - `"version"`/`"-v"`/`"--version"` → 打印 `version.String()` 返回 0；
  - `login/list/search/get/reveal/status/add/edit/totp/export/import/rotate-key/
    help/-h/--help` → 透传 `cli.Run(args)` 的返回码；
  - `"mcp"` → 透传 `mcpserver.Run()` 的返回码；
  - 其余子命令 `log.Fatalf`（退出码 2）。
- `buildServer` 的失败路径与路由结构：
  - 失败三类：主密钥加载失败、数据库打开失败、key_check 不匹配；
  - 成功时日志打印版本与主密钥来源 `keySrc.Desc`；
  - `/api/v1` 挂人用 API（`api.Server.Routes`）；
  - `/api/v1/ai` 按三组 `auth.RequireToken(database, scope)` 中间件分组：
    `read` 组（GET `/entries`、`/entries/{id}`）、`search` 组（GET `/search`）、
    `reveal` 组（POST `/entries/{id}/reveal`）；
  - `/*` 交给 `spaHandler`（`fs.Sub(web.Dist, "dist")`）。
- `runServe`：`KEYHIVE_ADDR` 默认 `:8020`，`KEYHIVE_DATA` 默认 `./data`；
  必须用 `srv.ListenAndServe()` 让 buildServer 里设置的超时生效
  （`http.ListenAndServe` 会另建零配置 server）。

## 依赖关系

- 依赖：`internal/{aiapi,api,auth,cli,crypto,db,mcpserver,store,version}` 与
  `web`（`go:embed all:dist` 前端产物）；chi v5 路由与中间件。
- 被依赖：无——它是所有内部包的组装根（composition root），无内部包反向引用。
- 数据流一句话：HTTP 请求 → chi 中间件（RequestID/RealIP/Recoverer + `/api/`
  路径耗时日志）→ api/aiapi handler → store → db/crypto。

## 安全与不变量

- 主密钥与库文件分离；`ensureKeyCheck` 保证"密钥与数据目录不匹配则拒绝启动"——
  首启写入 `c.KeyCheck()` 密文，此后每次启动 `VerifyKeyCheck` 比对，失败报
  "主密钥与数据库内 key_check 不匹配"并带处置提示（用原密钥启动或清空数据目录）。
- `ReadHeaderTimeout: 10s` 防慢速头部攻击，`IdleTimeout: 120s` 回收闲置连接；
  **不设 `WriteTimeout`**——否则会把 SSE 长响应（AI 聊天流）中途切断。
- SPA 缓存策略：
  - `/` 与 `index.html` 强制 `Cache-Control: no-cache`——入口必须每次校验，
    否则发版后浏览器仍按旧 index.html 加载旧资源；
  - `assets/` 命中文件给 `public, max-age=31536000, immutable`（文件名带 hash）；
  - embed 文件无 modtime，`http.FileServer` 不发 Last-Modified/ETag，不显式
    设头浏览器会全量重拉；
  - 未命中路径一律回退 `/`（前端路由），回退响应同为 no-cache。
- gzip 仅包在静态资源 handler 上（`middleware.Compress(5)`，1.8MB JS 压到约
  450KB）；API/SSE 不走这个 handler，不受压缩影响。
- 访问日志只打 `/api/` 前缀的 method/path/耗时，不打印请求体，明文密钥不出现在
  启动与访问日志中。

## 测试锚点

- `main_test.go`：
  - `TestE2EFullJourney` 以 `buildServer` 为可测形态跑登录→CRUD→遮蔽/reveal→
    TOTP→模板→令牌→AI API→导入导出→轮换→审计全链路；
  - `TestEnsureKeyCheck` 锚定首启写入与换密钥报"不匹配"；
  - `TestSPAHandlerDirect` 锚定 assets 长缓存头、根路径 no-cache、未命中回退 no-cache；
  - `TestE2EStaticAssets`/`TestBuildServerErrorPaths`/`TestEnvOr` 锚定静态访问、
    密钥篡改与非法目录失败路径、环境变量 trim。
- `serve_test.go`：`TestRunServeSmoke` 真实起服务探活 `/api/v1/version` 并断言
  `master.key` 与 `keyhive.db` 已生成；`TestDispatch` 锚定各子命令返回码
  （default 分支 log.Fatalf 不可测，跳过）。
