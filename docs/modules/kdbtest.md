# kdbtest 模块（internal/kdbtest）

## 职责

集成测试夹具包：一份 `New(t)` 就绪“内存 SQLite + 随机主密钥的 store + 预建 read/reveal 两级 AI 令牌 + 首启种子（admin 账号、31 个内置模板）”，供 `internal/aiapi` 与 `internal/api` 的 handler 级测试直接使用；另有 `SetPassword` 绕过 API 重置密码。它解决的是“handler 测试需要真实库与真实令牌”的重复装配问题，不参与任何运行时代码路径。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/kdbtest/fixture.go` | `Fixture` 结构体、`New(t)` 夹具装配、`SetPassword` 测试辅助 |
| `internal/kdbtest/fixture_test.go` | `TestFixtureSmoke`：夹具自检（库可写、令牌可建、改密生效） |

## 对外接口

- `type Fixture struct { DB *sql.DB; Store *store.Store; ReadToken, RevealToken string }`：
  - `ReadToken` scopes 为 `["read"]`；`RevealToken` scopes 为 `["read","search","reveal"]`——两者都是明文令牌，测试直接用作 `Authorization: Bearer` 值；
  - `DB` 可直接 Exec/Query 造数据或断言（如检查审计行）。
- `New(t *testing.T) *Fixture` — 装配步骤：
  1. `kdb.Open(":memory:")` 打开内存库（含建表迁移 + `seed`：admin 账号随机密码、内置模板、`settings.seeded`），`t.Cleanup` 自动关库；
  2. `store.NewForTest(db)` 挂随机 32 字节主密钥；
  3. `auth.CreateAPIToken` 预建上述两枚令牌（哈希真实落库）。
- `SetPassword(t, username, password)` — 直接 UPDATE `users.password_hash`（经 `auth.HashPassword`），等价管理员初始化：绕过改密 API 的旧密码校验与踢会话副作用，测试无需知道首启随机密码。

典型用法（aiapi 的 `newTestServer`：夹具 + 与 main 相同结构的挂载 + httptest）：

```go
fx := kdbtest.New(t)
ai := &aiapi.Server{Store: fx.Store}
r := chi.NewRouter()
r.Route("/api/v1/ai", func(ar chi.Router) {
    g := ar.Group(func(g chi.Router) { ... })
    g.Use(auth.RequireToken(fx.DB, "read"))   // read/search/reveal 三组同 main
    g.Get("/entries", ai.ListEntries)
})
srv := httptest.NewServer(r)                  // 请求头带 Bearer fx.RevealToken
```

## 依赖关系

- 依赖：`internal/db`（Open/migrate/seed）、`internal/store`（NewForTest）、`internal/auth`（CreateAPIToken/HashPassword）。
- 被依赖：`internal/aiapi`（aiapi_test.go、aiapi_edge_test.go）、`internal/api`（api_test.go、api_errors_test.go）。
- **起真实服务的方式不在本包**：全链路 e2e 在 `cmd/keyhive/main_test.go`（见下节）——两套形态互补：kdbtest 覆盖 handler+DB 层，e2e 覆盖含主密钥加载/路由组装的完整进程形态。

## 安全与不变量

1. **测试隔离**：每个测试独立内存库 + 独立随机主密钥，测试之间零共享、零残留；`t.Cleanup` 关闭 DB——Windows 下必须先关库句柄，`t.TempDir` 才能删除。
2. **真实而非 mock**：预建令牌是真实 `api_tokens` 行（SHA-256 哈希落库），中间件、scope 校验、审计路径与生产行为完全一致。
3. **起真实服务的方式**（`cmd/keyhive/main_test.go` 的 `newE2E`，四步）：
   1. `t.Setenv("KEYHIVE_MASTER_KEY", "")` 与 `t.Setenv("KEYHIVE_KEYFILE", "")` 清空密钥环境（避免宿主机密钥泄漏进测试）；
   2. `t.TempDir()` 作为数据目录；
   3. `buildServer(dataDir)`——生产 `runServe` 的可测形态：`crypto.LoadMasterKey` 加载/生成主密钥 → `kdb.Open` 打开/迁移库 → `ensureKeyCheck` 校验密钥匹配 → 组装 /api/v1（人用）、/api/v1/ai（三 scope）、SPA 回退全部路由，返回 `*http.Server` + `*sql.DB`；
   4. `httptest.NewServer(srvObj.Handler)` 在 127.0.0.1 随机端口监听真实 HTTP；测试用带 cookiejar 的客户端走 `POST /auth/login` 换会话。
4. 夹具不注入任何固定凭据：admin 初始密码由 seed 随机生成且只打日志；需要已知密码时用 `SetPassword`（e2e 用 SQL 直改 hash 等价实现），避免真实弱密码进代码库。

## 测试锚点

- `TestFixtureSmoke`：夹具自检——`CreateEntry` 可写、`SetPassword` 后哈希变化、令牌非空。
- 下游代表用例：aiapi 的 `TestRevealRequiresScopeAndWritesAudit`（夹具 + 三 scope）、api 的 `TestNonAdminDeniedOnAllAdminEndpoints`。
- 全链路：`cmd/keyhive` 的 `TestE2EFullJourney`——登录 → 条目 CRUD → 遮蔽/reveal → TOTP → 模板另存 → AI 令牌 → AI API → 导出导入 → 轮换 → 审计，全程走真实 HTTP 端口；`main_test.go` 另有 `buildServer` 的 key_check 不匹配拒启测试。
