# api 模块（internal/api）

## 职责

人用 HTTP API（挂载于 `/api/v1`，chi + session cookie 认证）：登录会话、条目/分类/模板 CRUD、TOTP 生成、AI 令牌与用户管理、审计分页、CSV/JSON 导入、明文与遮蔽导出、主密钥轮换、LLM 配置与 SSE 聊天代理。当前工作区共注册 **37 条路由、36 个 handler 方法**（`handleAIConfig` 一个方法同时挂 GET 与 PUT）。统一请求体上限 10MB、5xx 只回通用文案。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/api/api.go` | `Server`、`Routes` 全量路由、`limitBody`/`readBody`/`writeErr`/`writeInternalErr`、登录/条目/导出 handler |
| `internal/api/admin.go` | 模板、AI 令牌、审计分页 handler |
| `internal/api/users.go` | 用户 CRUD 与“最后一个管理员”护栏 |
| `internal/api/rotate.go` | `rotateKey` 主密钥轮换端点 |
| `internal/api/webui.go` | 版本、TOTP、导入、stale-days 设置 |
| `internal/api/aichat_api.go` | LLM 配置（settings 表加密存储）、AI 聊天与 SSE 流、测试连接、模型列表 |

## 对外接口

`Server{Store *store.Store}` + `Routes(r chi.Router)`（由 `cmd/keyhive.buildServer` 挂到 `/api/v1`）。内部纪律函数：`maxBodyBytes = 10 << 20`；`limitBody` 全子路由器生效（Content-Length 超限直接 413，chunked 由 `readBody` 识别 `*http.MaxBytesError` 后经 `writeReadErr` 映射 413）；`writeInternalErr` 5xx 完整错误只进服务端日志，客户端只收「服务器内部错误」。

### 路由表（路径相对 `/api/v1`；所有行均先过 `limitBody`）

| 端点 | 中间件 | 语义 |
|---|---|---|
| POST /auth/login | 公开 | 账密换会话 cookie；失败/禁用记 `login_failed` 审计 |
| POST /auth/logout | 公开 | 删会话并清 cookie |
| GET /version | 公开 | 返回 `version.Version` |
| GET /auth/me | Session | 当前用户 id/username/is_admin |
| POST /auth/logout-all | Session | 踢掉自己全部会话 |
| POST /password | Session | 改密（≥8 位）成功后踢全部会话，记审计 |
| GET /entries?q=&category= | Session | 列表/搜索，敏感值一律遮蔽 `***` |
| POST /entries | Session | 建条目：校验+加密+审计，返回遮蔽版（201） |
| GET /entries/{id}?reveal=true | Session | 详情默认遮蔽；reveal=true 返回明文并记 `entry_reveal` 审计 |
| PUT /entries/{id} | Session | 更新；`***` 哨兵保留旧值（store 层还原） |
| DELETE /entries/{id} | Session | 删除并记审计 |
| POST /entries/{id}/save-as-template | Session | 条目另存模板（只带走骨架与注释，值清空） |
| GET /categories | Session | 已用分类去重列表 |
| POST /entries/{id}/totp | Session | 服务端解密 TOTP 密钥生成动态码，记审计 |
| GET /settings/stale-days | Session | 读轮换提醒阈值（默认 90 天） |
| GET /templates | Session | 模板全量列表 |
| POST /templates | Session | 新建模板并记审计 |
| PUT /templates/{id} | Session | 更新模板（内置亦可改名/改字段） |
| DELETE /templates/{id} | Session | 删除模板；`Builtin` 拒绝 400「内置模板不可删除」 |
| GET /ai-config | Session | LLM 配置；api_key 遮蔽只露尾 4 位（`maskedKey`） |
| POST /ai-chat | Session | AI 对话录入（非流式，502 转发上游错误） |
| POST /ai-chat/stream | Session | AI 对话 SSE 流：status/reply_delta/draft/error/done |
| GET /users | Session+Admin | 用户列表 |
| POST /users | Session+Admin | 建用户（密码≥8 位、用户名唯一） |
| PUT /users/{id} | Session+Admin | 改密/禁用/升降权；唯一管理员禁用/降权被拒；改密/禁用即踢会话 |
| DELETE /users/{id} | Session+Admin | 删用户（不能删自己、不能删最后一个管理员） |
| GET /tokens | Session+Admin | AI 令牌列表（不含哈希） |
| POST /tokens | Session+Admin | 建令牌：scope 白名单 read/search/reveal、read 隐含；明文仅本次响应 |
| DELETE /tokens/{id} | Session+Admin | 吊销令牌 |
| GET /audit?limit&offset&action&with_total | Session+Admin | 审计分页；with_total=1 返回 {total, logs} |
| GET /export?masked=1 或 true | Session+Admin | 全库导出；masked 时服务端 `MaskSecrets()` 遮蔽；每次记 `export` 审计（含 count/masked） |
| POST /rotate-key | Session+Admin | 主密钥轮换：重加密全部条目+settings，返回 entries/key_file 或 new_master_key |
| POST /import | Session+Admin | 导入 {format, csv, dry_run}：bitwarden/chrome 走 `importer.Parse`，keyhive 走 `importKeyhive`；dry_run 只回 count+preview(≤20) |
| PUT /settings/stale-days | Session+Admin | 写阈值（1-3650 校验） |
| PUT /ai-config | Session+Admin | 保存 LLM 配置；api_key 以 `****` 开头视为未修改，否则原文加密落库 |
| POST /ai-config/test | Session+Admin | 测试 LLM 连通性（表单值优先，空/遮蔽 Key 回退已存值） |
| POST /ai-config/models | Session+Admin | 拉取服务商模型清单（本地端点无 Key 也可） |

## 依赖关系

依赖 `internal/store`、`internal/auth`、`internal/audit`、`internal/model`、`internal/aichat`、`internal/importer`、`internal/totp`、`internal/version`；被 `cmd/keyhive.buildServer` 组装。AI 专用面（Bearer）在 `internal/aiapi`，不在本包。

## 安全与不变量

1. **10MB MaxBytesReader 覆盖整个 /api/v1 子路由器**（含未认证的 /auth/login，防未登录内存耗尽）；SSE 长响应不受请求体限制影响。
2. **遮蔽纪律**：所有读路径（list/get/update/create 响应）出站前 `MaskSecrets()`；明文只在 `?reveal=true`（记审计）与 admin 明文导出两处出现。LLM api_key 永不明文回显。
3. **/ai-config/test 与 /ai-config/models 位于 RequireAdmin 组**：二者会解密已保存 Key 并向用户自填地址发起外联，仅管理员可用（`TestBug_AIConfigTestRequiresAdmin`、`TestNonAdminDeniedOnAllAdminEndpoints`）；聊天与 GET 配置登录即可。
4. **审计 detail 一律 `audit.Detail()` 构造**：标题/字段名含双引号时手工拼 JSON 会破坏取证契约（`TestBug_AuditDetailValidJSON`）；登录失败也落审计（用户名记入 actor_name）。
5. **错误纪律**：4xx 业务校验错误可带具体中文文案；5xx 一律 `writeInternalErr` 只回通用文案；请求体解码失败 `writeReadErr`（超限 413 / 格式 400）。
6. 用户管理护栏：唯一有效管理员不可禁用/降级/删除（`countAdmins` 实时计数）；不能删自己。
7. SSE 响应头 `text/event-stream` + `Cache-Control: no-cache` + `X-Accel-Buffering: no`；上游出错以 `error` 事件收尾。

## 测试锚点

`TestBug_MaxBytesRejected`、`TestBug_AIConfigTestRequiresAdmin`、`TestBug_AuditDetailValidJSON`、`TestNonAdminDeniedOnAllAdminEndpoints`、`TestExportMaskedParam`、`TestAuditPagination`、`TestRotateKeyFileMode` / `TestRotateKeyEnvMode`、`TestImportKeyhiveRoundTrip`、`TestAIChatStreamWithDraft`、`TestHandler500Paths`、`TestLoginEdgeCases`、`TestUserManagementLifecycle`。
