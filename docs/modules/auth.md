# auth 模块（internal/auth）

## 职责

keyHive 的认证与授权核心：argon2id 密码哈希、管理员会话（cookie）生命周期、AI 令牌（Bearer）的创建/校验/吊销，以及三件 chi 中间件 `RequireSession` / `RequireAdmin` / `RequireToken`，分别支撑人用 API（/api/v1）与 AI 用 API（/api/v1/ai）两套路由的权限边界。会话与令牌在库中只存 SHA-256 哈希，明文只在创建瞬间返回一次。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/auth/password.go` | `HashPassword` / `VerifyPassword`（argon2id 编解码与常数时间比较） |
| `internal/auth/store.go` | 会话与 AI 令牌的全部 DB 操作（sessions / api_tokens 表） |
| `internal/auth/middleware.go` | 三个中间件、context key、`UserID` / `TokenInfo` 取值 |
| `internal/auth/auth_full_test.go`、`auth_edge_test.go`、`password_test.go` | 全生命周期、畸形输入与边界回归 |

## 对外接口

- **密码（password.go）**：
  - `HashPassword(password) (string, error)`：argon2id，参数 `argonTime=1`、`argonMemory=64MB`、`argonThreads=4`、`argonKeyLen=32`、随机盐 16 字节；输出格式 `$argon2id$v=19$m=65536,t=1,p=4$salt$hash`（base64 RawStd）；
  - `VerifyPassword(password, encoded) bool`：从编码串解析 version/m/t/p 与盐/密文，重算后 `subtle.ConstantTimeCompare` 比较；任何一步解析失败返回 false（不报错、不 panic）。
- **会话（store.go）**：
  - `SessionTTL = 7 * 24 * time.Hour`；`SessionRecord` 含 UserID/IP/UserAgent/时间戳；
  - `CreateSession(db, userID, ip, ua) (明文token, *SessionRecord, error)`——库里只存 `hashToken(明文)` 的 SHA-256；
  - `GetSession(db, token) (*SessionRecord, error)`：查哈希 → 校验 `expires_at` → **滑动续期**（刷新 `last_seen_at` 与 `expires_at`）；失败统一 `ErrSessionInvalid`；
  - `DeleteSession`（登出单个）、`DeleteUserSessions`（踢该用户全部，改密/禁用后调用）。
- **AI 令牌（store.go）**：
  - `CreateAPIToken(db, name, scopes, expiresAt) (*model.APIToken, 明文, error)`：明文为 `"kh_" + RandomToken()`，库里只存哈希；
  - `GetAPIToken(db, plaintext)`：哈希命中 → `revoked_at` 为空 → `expires_at` 未过（RFC3339 解析失败视为过期），失败统一 `ErrTokenInvalid`；
  - `TouchAPIToken`（更新 last_used_at）、`RevokeAPIToken`（置 revoked_at，仅首个吊销生效）、`ListAPITokens`（列表不含哈希）。
- **中间件（middleware.go）**：
  - `RequireSession(db)`：cookie `keyhive_session` → `GetSession` → context 写入 `CtxUserKey`；失败 401 JSON；
  - `RequireAdmin(db)`：取 `UserID` 后**每请求实时查库**复核 `is_admin`/`disabled`（禁用/降权即时生效，不信任会话创建时刻），未登录 401、账号不可用 401、非管理员 403；
  - `RequireToken(db, scope)`：`Authorization: Bearer ` 前缀 → `GetAPIToken` → scope 白名单（`apiTokenCtx.Has`）→ `TouchAPIToken` → context 写入 `CtxTokenKey`。
- **取值与常量**：`UserID(ctx)`、`TokenInfo(ctx)`（token id/name/scopes）、`CookieName`、`RandomToken()`（32B crypto/rand → base64 RawURL，也被 db.seed 用于 admin 初始密码）。

导出符号速查：

| 符号 | 类别 | 说明 |
|---|---|---|
| `HashPassword` / `VerifyPassword` | 密码 | argon2id 编解码 + 常数时间比较 |
| `CreateSession` / `GetSession` / `DeleteSession` / `DeleteUserSessions` | 会话 | cookie 明文 ↔ 库内哈希 |
| `SessionTTL` / `SessionRecord` / `ErrSessionInvalid` | 会话 | 7 天 TTL、记录结构、统一失效错误 |
| `CreateAPIToken` / `GetAPIToken` / `RevokeAPIToken` / `ListAPITokens` / `TouchAPIToken` | AI 令牌 | `kh_` 前缀、哈希落库 |
| `ErrTokenInvalid` | AI 令牌 | 统一失效错误（吊销/过期/不存在同文案） |
| `RequireSession` / `RequireAdmin` / `RequireToken` | 中间件 | 人用认证 / 管理员复核 / Bearer+scope |
| `CookieName` / `RandomToken` | 常量与工具 | cookie 名与 URL-safe 随机令牌 |

## 依赖关系

- 依赖：`golang.org/x/crypto/argon2`、`internal/model`（`APIToken`）；DB 访问只用 `database/sql`，不绑定驱动。
- 被依赖：`internal/api`（登录与会话/令牌/用户管理、两个路由组）、`cmd/keyhive`（AI API 三组按 read/search/reveal 挂载）、`internal/kdbtest`（预建令牌）、`internal/db`（首启 admin 密码哈希与随机密码）。

## 安全与不变量

1. **畸形哈希不 panic、不恒真**：`VerifyPassword` 前置校验 `t/p/m == 0`、盐或密文长度为 0 一律判失败——参数为 0 会让 `argon2.IDKey` 直接 panic，空密文会得到恒等比较（空密文恒“通过”）（`TestBug_VerifyPasswordMalformedNoPanic`）。
2. **哈希落库、明文一次性**：会话 id 与 `api_tokens.token_hash` 均为 SHA-256；令牌明文只在 `CreateAPIToken` 返回值与 `POST /tokens` 响应（`Token` 字段）中出现一次。
3. **fail-closed**：会话/令牌校验中任何错误（DB 错误、时间解析失败、context 缺值）都按无效处理；滑动续期的 UPDATE 失败同样返回错误而非放行（`TestSessionRenewKeepsFailClosedOnGarbage`）。
4. **吊销/过期/禁用即时生效**：`RequireAdmin` 每请求查库；令牌 revoked/expired 即刻 401；改密与禁用后 api 层调 `DeleteUserSessions` 强制全端下线。
5. **错误文案纪律**：中间件统一回 `{"error":"..."}`——未登录/会话过期/令牌无效 401，需要管理员 403，缺 scope 403（错误串内含所需 scope 名）；不泄露失败原因细节。
6. `TouchAPIToken` 在每次 Bearer 请求成功后异步式更新（忽略错误），供令牌页展示 last_used_at。

## 测试锚点

`TestBug_VerifyPasswordMalformedNoPanic`、`TestVerifyMalformed`、`TestHashUniquePerCall`（同密码两次哈希不同）、`TestSessionSlidingRenewalMovesExpiry`、`TestSessionRenewKeepsFailClosedOnGarbage`、`TestSessionExecError`、`TestAPITokenLifecycle`、`TestRequireTokenMiddleware`、`TestRequireAdminMiddleware`、`TestRequireSessionMiddleware`、`TestBearerSchemeEdges`（非 Bearer scheme 与大小写边界）、`TestSessionLifecycle`。
