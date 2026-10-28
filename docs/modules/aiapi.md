# aiapi 模块（internal/aiapi）

## 职责

AI 专用 API（挂载于 `/api/v1/ai`，Bearer 令牌认证）：面向 ZCode / Claude 等 AI 客户端与 `keyhive` CLI / MCP 的机器接口。四个 handler 按 read / search / reveal 三级 scope 由 `cmd/keyhive` 分别挂载——read 面返回遮蔽结构与完整注释，search 面按关键词过滤，reveal 面单字段取明文并每次落审计。`ai_visible=false` 的条目对全部端点完全不存在。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/aiapi/aiapi.go` | `Server` 与四个导出 handler（`ListEntries` / `GetEntry` / `Search` / `Reveal`） |
| `internal/aiapi/aiapi_test.go` | 三 scope 主路径：遮蔽、隐身、审计 |
| `internal/aiapi/aiapi_edge_test.go` | 边界：空参、损坏行、关闭库、reveal 非敏感字段 |
| `internal/aiapi/search_edge_test.go` | 搜索边界：fields JSON 命中、LIKE 通配、空结果数组 |
| `internal/aiapi/aiapi_bug_test.go` | reveal 审计 detail 合法 JSON 回归 |

> 挂载代码在 `cmd/keyhive/main.go` 的 `buildServer`：read/search/reveal 三组 `ar.Group` + `auth.RequireToken(database, <scope>)`，本包不自带路由注册。

## 对外接口

`Server{Store *store.Store}`；handler 均为导出方法，由 `buildServer` 按不同 scope 分组挂载：

| 端点（绝对路径） | scope 中间件 | 语义 |
|---|---|---|
| GET /api/v1/ai/entries | `auth.RequireToken(db, "read")` | 全部 AI 可见条目，敏感值遮蔽 `***`，description 注释原样 |
| GET /api/v1/ai/entries/{id} | `auth.RequireToken(db, "read")` | 单条遮蔽详情；隐身/不存在条目均 404 |
| GET /api/v1/ai/search?q= | `auth.RequireToken(db, "search")` | 关键词模糊匹配（复用 `store.ListEntries`）；q 必填，空参 400 |
| POST /api/v1/ai/entries/{id}/reveal | `auth.RequireToken(db, "reveal")` | body `{"field":"字段名"}` → 单字段明文 + 元信息 |

- `Reveal` 响应体：`{entry_id, entry_title, key, description, value}`；条目或字段不存在 404，body 缺 field 或解码失败 400。
  - `description` 一并返回：调用方（AI/脚本）拿到明文的同时能看到字段用法注释；
  - 审计先于响应写入：`audit.Log` 在 `writeJSON` 之前执行，取明文必留痕。
- 错误统一 `{"error": "..."}` JSON：缺 Bearer / 令牌无效 401、缺 scope 403（由 auth 中间件回）、DB 错误 500。
- `Reveal` 之外的三个面是纯 GET、幂等、不写审计——令牌枚举条目结构不算敏感事件，只有取明文才落审计（与人工 `?reveal=true` 的 `entry_reveal` 分列：AI 用 `field_reveal`）。
- 身份取值：`tokenInfo(r)` 经 `auth.TokenInfo(r.Context())` 拿令牌 id/name 用于审计；`parseInt64` 拒绝非数字 id。

与 CLI / MCP 的对应关系（同一后端的三个前脸）：

- `keyhive list/search/get/reveal` → 上述四端点（token_read / token_reveal）；
- MCP 的 `kh_list/kh_search/kh_get/kh_reveal` → 复用 cli 调用函数 → 同四端点；
- 因此遮蔽、隐身过滤、reveal 审计三套纪律对三个前脸一次性生效，MCP/CLI 无法绕过。

## 依赖关系

- 依赖：`internal/store`（`ListEntries`/`GetEntry`）、`internal/auth`（`TokenInfo`）、`internal/audit`、`internal/model`、chi。
- 被依赖：`cmd/keyhive`（挂载三组）；是 `internal/cli` 与 `internal/mcpserver` 的服务端对端。
- 测试依赖 `internal/kdbtest.New`（内存库 + 预建 read/reveal 令牌 + store）。

## 安全与不变量

1. **遮蔽默认**：`ListEntries` / `GetEntry` / `Search` 三个面出站前逐条 `MaskSecrets()`（`***`）；description（给 AI 的注释）与字段名永不遮蔽——这是“字段名+注释足够完成任务时不取明文”纪律的数据基础（`TestListMasksSecretsAndHidesInvisible`）。
2. **条目级隐身**：所有端点对 `!AIVisible` 的条目一律 `404 条目不存在`（与不存在同响应，不泄露存在性）；list/search 结果循环中直接过滤（`TestGetEntryInvisible404`、`TestSearchFiltersInvisible`）。
3. **reveal 全程审计**：每次 `Reveal` 以 `actor_type="token"`、令牌 id/name、`model.ActionFieldReveal`、`entry_id` 落审计，IP 取 `r.RemoteAddr`；detail 一律 `audit.Detail(map[string]any{...})` 构造——title/字段名可含双引号，手工拼 JSON 会产出非法结果破坏取证契约（`TestBug_RevealAuditDetailValidJSON`）。
4. **非敏感字段也可 reveal**：按字段名取值，不校验 `IsSecret`——契约是“取单字段明文即审计”，敏感与否不影响管控强度；缺失字段 404 带 field 名。
5. **空结果恒为数组**：list/search 空时返回 `[]` 而非 `null`（`TestSearchEmptyResultAlwaysArray`）。
6. **scope 分离由挂载点决定**：search 端点不因令牌有 read 就放行（`RequireToken` 按 scope 名白名单，`TestSearchScopeSeparate`）；reveal 面独立成组（`TestRevealRequiresScopeAndWritesAudit`）。
7. 坏行容错继承 `store.ListEntries`：单条损坏跳过、全军覆没才报 500（`TestListEntriesEmptyAndCorrupt`）。

## 测试锚点

`TestListMasksSecretsAndHidesInvisible`、`TestRevealRequiresScopeAndWritesAudit`、`TestBug_RevealAuditDetailValidJSON`、`TestSearchScopeSeparate`、`TestSearchMatchesFieldsJSON`（fields JSON 文本可被 LIKE 命中）、`TestSearchLikeWildcard`、`TestSearchEmptyQuery`、`TestGetEntryInvisible404`、`TestRevealEdgeCases`、`TestRevealNonSecretAndMissingField`、`TestListEntriesEmptyAndCorrupt`、`TestClosedDBHandlers`。
