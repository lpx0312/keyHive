# 认证（auth）代码审查报告

> 审查日期：2026-10-28 · 审查人：安全代码审查（只读） · 范围：`internal/auth/`（password.go、store.go、middleware.go、password_test.go、auth_full_test.go），并核对调用方上下文（internal/api/api.go、users.go、admin.go、cmd/keyhive/main.go、internal/db/db.go、internal/model/model.go）以验证 cookie 属性、改密后会话处理、路由 scope 绑定。

## 概览

- 范围：auth 包 5 个文件全部通读，另核对 6 个调用方文件中的相关片段。
- 总体评价：认证模块设计与实现质量高于同类自托管项目平均水平。令牌/会话只存 SHA-256 哈希、明文仅创建时返回一次；argon2id 参数稳妥且采用 PHC 自描述格式；三 scope 中间件按路由分组挂载，read 令牌无法触达 search/reveal 端点；错误响应三态合并，无法枚举令牌有效性；改密/禁用/删除用户均踢全部会话。未发现认证绕过、scope 提权或令牌枚举类问题。本次发现集中在边界健壮性与部署属性上：1 条 P2（会话 cookie 缺 Secure 且无开关）、7 条 P3（畸形哈希参数 panic、会话无绝对寿命上限、过期数据无清理、登录时序差等）。两条关于 argon2 畸形参数行为的结论（t=0/p=0/空 hash 段 panic、m=0 被静默 clamp）已在隔离环境用同版本 x/crypto v0.57.0 实证，非臆测。

## 发现清单

**P0（阻断）：无。P1（严重）：无。**

---

**[P2] 会话 cookie 未设置 Secure 属性，且无配置开关**（internal/api/api.go:162-168）

- 问题：登录时 `http.SetCookie` 只设置了 `HttpOnly: true` 与 `SameSite: Lax`，没有 `Secure`，也没有任何按部署形态（HTTP 内网 / HTTPS 反代）切换的配置项。
- 影响：keyHive 是密钥管理器，会话 cookie 等价于全库读取权。一旦实例经 HTTPS 反代对外暴露（该用户确有此部署习惯），同一 host 上的明文 HTTP 请求、或浏览器历史中的 `http://` URL 都会以明文携带该 cookie。内网纯 HTTP 部署下直接加 Secure 会导致 cookie 不可用，因此需要的是开关而非无条件加上。
- 修复建议：增加配置（如 `KEYHIVE_SECURE_COOKIE=1` 或监听 TLS 时自动启用），启用时对登录及所有清除 cookie 的 `SetCookie`（api.go:176、183、233）统一设置 `Secure: true`。

**[P3] VerifyPassword 对畸形 argon2 参数 panic 而非返回 false**（internal/auth/password.go:59）

- 问题：`VerifyPassword` 解析 PHC 串后未校验参数取值，直接调用 `argon2.IDKey`。实证（x/crypto v0.57.0）：`t=0` panic "number of rounds too small"、`p=0` panic "parallelism degree too low"、hash 段为空串时 `len(want)=0` 导致 blake2b nil 解引用 panic；`m=0` 则被库内静默 clamp 到 `8*p` 后正常计算（返回 false）。全局 `middleware.Recoverer`（main.go:107）会把 panic 转成 500，进程不崩。
- 影响：前提是 `users.password_hash` 被写成畸形串（手工 SQL、迁移事故、DB 损坏），此时登录返回 500 而非明确的验证失败；`m=0` 场景还会白白跑一次弱参数哈希。属 fail-loud 的健壮性缺陷，不构成可利用绕过。
- 修复建议：解析完成后校验 `t >= 1 && p >= 1 && p <= 255 && m >= 8*p && len(want) >= 16`，不满足直接 `return false`；并在 password_test.go 补参数级畸形用例。

**[P3] 会话滑动续期无绝对寿命上限，且记录的 IP/UA 从未被使用**（internal/auth/store.go:92-94）

- 问题：`GetSession` 每次把 `expires_at` 续到 `now+7d`，只要持续使用会话永不过期。同时 `SessionRecord.IP/UserAgent` 落库后既不与后续请求比对，也不参与任何告警，纯静态留存。
- 影响：cookie 一旦被盗，攻击者保持周期性访问即可无限续期，"7 天 TTL"名存实亡；被盗用也无异常轨迹可查。内网个人服务器风险可接受，但与密钥管理器的定位不匹配。
- 修复建议：增加绝对寿命上限（如 `created_at` 超过 30 天即失效，续期只影响 `expires_at` 两者取小）；IP/UA 突变至少记审计。

**[P3] 过期会话与过期/吊销令牌无清理任务**（internal/auth/store.go 全文）

- 问题：过期判断只在读取时进行，`sessions`/`api_tokens` 中过期、已登出之外的残留行（含 IP、UA）永不删除；全仓库无任何 `expires_at <` 清理语句（已搜索确认）。
- 影响：数据库无限增长；会话 IP/UA 等痕迹数据长期留存，放大隐私与取证面。
- 修复建议：启动时或定时执行 `DELETE FROM sessions WHERE expires_at < now`、`DELETE FROM api_tokens WHERE revoked_at IS NOT NULL OR expires_at < now`（吊销行保留期可配置）。

**[P3] 登录接口时序差可枚举有效用户名**（internal/api/api.go:144）

- 问题：`err != nil || !auth.VerifyPassword(req.Password, hash)` 短路求值——用户不存在时（`err != nil`）跳过 argon2 计算（微秒级返回），用户存在时执行 64MB argon2（数十毫秒），时序差异远大于网络抖动。错误文案虽统一为"用户名或密码错误"，时序却出卖了存在性。
- 影响：内网部署、账号数量少（默认必有 admin），实际价值有限，故仅 P3。
- 修复建议：用户不存在时对一个固定的哑哈希执行一次 `VerifyPassword` 再统一拒绝，抹平时序。

**[P3] Bearer scheme 大小写敏感，非规范写法一律拒绝**（internal/auth/middleware.go:99-103）

- 问题：`strings.HasPrefix(h, "Bearer ")` 严格区分大小写；`bearer xxx` 返回 401"缺少 Bearer 令牌"。RFC 6750 规定 auth-scheme 匹配应大小写不敏感。另外 `Bearer␣␣token`（双空格）解析出的令牌带前导空格，hash 不匹配，同样 401。
- 影响：所有边界行为均 fail closed，无安全危害，纯互操作性/规范符合度问题；某些按 RFC 实现的客户端发小写 scheme 会被拒。
- 修复建议：按空格分割后 `strings.EqualFold(parts[0], "bearer")`，取 `TrimSpace` 后的余部作为令牌。

**[P3] apiTokenCtx.Has 与 model.APIToken.HasScope 重复实现，前者为死代码**（internal/auth/middleware.go:58-71）

- 问题：`RequireToken` 实际调用的是 `model.APIToken.HasScope`（middleware.go:108）；`apiTokenCtx.Has` 在生产代码中无任何调用点，仅测试引用（auth_full_test.go:329）。同一 scope 判定逻辑存在两份。
- 影响：当前行为一致，但将来任一处修改（如支持 scope 通配或大小写）会造成中间件与测试断言所验证的逻辑脱节。
- 修复建议：删除 `apiTokenCtx.Has`，测试改为针对 `model.APIToken.HasScope` 或直接经中间件路径验证。

**[P3] AI 侧令牌认证失败无审计记录**（internal/auth/middleware.go:103-107）

- 问题：人类侧登录失败有 `login_failed` 审计（api.go:145），但 `RequireToken` 对无效/吊销/过期令牌返回 401 时不留任何日志或审计；`/api/v1/ai` 下的暴力尝试、已吊销令牌仍在被调用等异常完全不可追溯。
- 影响：256-bit 令牌暴力不可行，但"已吊销令牌仍被使用"往往是凭据泄露的最早信号，当前无法发现。
- 修复建议：401 分支按来源 IP 记审计（action 可复用 `login_failed` 或新增 `token_auth_failed`），注意限频避免刷库。

## 做得好的地方

1. **令牌/会话只存哈希，熵充足**：会话与 AI 令牌均为 32 字节 CSPRNG（256-bit 熵）经 base64url 编码，库中仅存 SHA-256 哈希（store.go:37-40、61-67、125-128），明文仅创建响应返回一次；256-bit 输入对 256-bit 输出的哈希无彩虹表/碰撞之忧，`token_hash UNIQUE` 约束兜底。`kh_` 前缀便于服务端识别与防跨系统混用。
2. **argon2id 配置与编码规范**：64MB/t=1/p=4/keyLen=32/16 字节随机盐（password.go:14-20），介于 RFC 9106 两档推荐配置之间，对个人服务器量级稳妥；PHC 标准格式把参数写进哈希串，`VerifyPassword` 按串解析（password.go:45-59），将来调参向后兼容；最终比较用 `subtle.ConstantTimeCompare`。
3. **scope 守卫闭环正确**：三个 scope 各挂独立中间件分组（main.go:122-136），`RequireToken(db, "search")` 对 read-only 令牌返回 403（测试覆盖）；`createToken` 服务端强制 scope 白名单且 read 隐含（admin.go:129-138），客户端传入的垃圾 scope 无法入库；令牌过期时间在入口做 RFC3339 校验，`GetAPIToken` 对非法时间串 fail closed。
4. **错误响应不泄露可枚举信息**：AI 令牌 401 将"无效/已吊销/已过期"三态合并为同一文案（middleware.go:105），无法通过响应区分探测；403 scope 提示仅对已持有有效令牌者可见，不构成信息泄露。人侧会话错误同样不回显内部细节（DB 错误只进日志）。
5. **生命周期事件处理完整**：改密、被禁用、被删除用户均踢全部会话（api.go:232、users.go:173-174、211），登录成功后新建会话令牌杜绝会话固定；时间全程 `time.Now().UTC()` + RFC3339 读写一致（store.go:42、52-58、88-94），无时区/单位混用，SQLite 连接开启 `foreign_keys(1)`（db.go:21）使 `ON DELETE CASCADE` 真实生效；`ListAPITokens` 查询不包含哈希列。

## 测试盲区

- **参数级畸形哈希未覆盖**：password_test.go 只测了非 argon2 格式与坏 base64；`t=0`/`p=0`/空 hash 段 panic、`m=0` 静默 clamp（均为本次实证行为）无测试锚点，重构解析逻辑时无回归护栏。
- **测试 schema 与生产 DDL 人工同步**：auth_full_test.go 自建内存表结构，db.go 中生产 DDL 的列变更（改名/加列）不会使 auth 测试失败，存在静默漂移风险。
- **无并发测试**：同一令牌并发 `GetSession` 触发并发 UPDATE（SQLite busy/WAL 行为）、`CreateAPIToken` 并发哈希碰撞（UNIQUE 冲突路径）均未覆盖。
- **Bearer 解析边界未覆盖**：小写 `bearer`、双空格、`"Bearer"`（无令牌）未入测试；当前 fail closed，但解析逻辑一旦调整无护栏。
- **滑动续期只断言 last_seen 变化**：未断言 `expires_at` 真的前移，也未断言续期上限策略（若按建议加绝对上限，需新锚点）。
- **cookie 属性无集成断言**：Set-Cookie 头内容（HttpOnly/Secure/SameSite）在 api 层设置，auth 测试够不到；无任何测试断言登录响应的 cookie 属性组合。
- **禁用用户残留会话防线**：`RequireSession` 本身不校验 `users.disabled`（middleware.go:74-92），当前正确性依赖 users.go:173 的踢会话调用——若将来有人移除该调用，无测试会失败。
