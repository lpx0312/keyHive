# 审计日志（internal/audit）

## 职责

提供"人 + AI 全行为"审计的写入与查询：`Log` 一行 INSERT 落 `audit_logs` 表
（登录成败、条目增删改、每次敏感值被人查看或 AI 取用、模板、令牌、用户管理、
导出、导入、密钥轮换、TOTP 取码等），`Count`/`List` 支撑管理端按动作筛选与
分页；`Detail` 用 `json.Marshal` 安全构造 detail 字段，保证审计记录永远是合法 JSON。

## 关键文件与入口

| 文件 | 关键函数/类型 | 说明 |
|---|---|---|
| audit.go | `Detail(v any) string` | `json.Marshal` 构造 detail；失败兜底返回 `"{}"` |
| audit.go | `Log(db, actorType, actorID, actorName, action, entryID *int64, detail, ip string)` | 写一条审计记录，错误被忽略 |
| audit.go | `Count(db, action string) (int, error)` | 总数统计，筛选条件与 `List` 一致，供分页页码计算 |
| audit.go | `List(db, action, limit, offset) ([]model.AuditLog, error)` | 按 action 筛选 + `ORDER BY id DESC` 分页查询 |
| audit.go | `nowUTC()` | RFC3339 UTC 时间戳（内部函数） |

## 对外接口

- `Detail(v any) string`
  - 调用方一律传 `map[string]any{...}` 让本函数序列化，禁止手拼 JSON 字符串：
    条目标题/字段名含双引号、反斜杠时字符串拼接会产出非法 JSON，破坏审计的
    取证契约。
  - `json.Marshal` 失败时返回 `"{}"`——宁可丢细节也不落非法 JSON。
  - 仓库内调用形态：`audit.Detail(map[string]any{"title": e.Title, "field": f.Key})`。
- `Log(...)`：fire-and-forget 写入
  - 签名无返回值，`db.Exec` 的错误被丢弃——审计失败不阻断主流程。
  - 空 `detail` 自动补 `"{}"`；`created_at` 由内部 `nowUTC()` 生成。
  - `entryID` 传 nil 表示动作不关联条目（如 login/export），非 nil 关联具体条目。
  - actor 三元组 `(actorType, actorID, actorName)` 取值词表为 `user | token | system`
    （`model.AuditLog.ActorType` 注释约定）。
  - action 参数必须取 `model` 包的 `Action*` 常量，不得手拼字符串。
- `Count(db, action)`：`action` 为空统计全量，否则与 `List` 用同一 `WHERE action = ?`，
  二者组合做分页不会口径错位。
- `List(db, action, limit, offset)`
  - limit 钳制：`limit <= 0 || limit > 200` 一律按 50 处理。
  - 结果按 `id DESC` 新在前。
  - `entry_id` 列 NULL 还原为 nil 指针、非 NULL 还原为 `*int64`。

## 依赖关系

- 依赖：`internal/model`（`AuditLog` 类型与 `Action*` 常量），标准库 `database/sql`/`encoding/json`。
- 被依赖：`internal/api`（api.go / webui.go / admin.go / users.go / aichat_api.go /
  rotate.go）与 `internal/aiapi`。
- 数据流一句话：api/aiapi handler 在写操作与 reveal 后同步调 `Log` 落表，
  管理端审计页 handler 调 `Count` + `List` 渲染；表结构由 `internal/db` 建表迁移维护。

## 安全与不变量

- 审计失败不阻断主流程是明确取舍：`Log` 吞掉写库错误，换取业务操作不因审计表
  故障失败；代价是审计记录可能静默丢失——审计数据只作事后取证，不作为访问
  控制的依据。
- detail 内容由调用方保证只含元数据（标题、字段名、数量、来源、成败计数），
  敏感字段值与明文密钥绝不进 detail；`Detail` 只负责格式合法，不负责脱敏。
- 每次 `entry_reveal`（人查看）与 `field_reveal`（AI 取用）必须落审计——这是
  "敏感值可审计地取用"安全模型的核心断言，由 api/aiapi 的调用点保证。
- `Log` 的 INSERT 用占位参数，无 SQL 注入面；`List` 的 action/limit/offset 同样
  走占位参数。
- `audit_logs` 建表与索引 `idx_audit_created(created_at DESC)` 归 `internal/db`，
  本包只读写不建表；两包的列清单必须保持一致。

## 测试锚点

`audit_test.go`（内存库自建 `audit_logs` 表，不依赖 db 包）：

- `TestLogAndList`：空 detail 补 `{}`、`id` 倒序、entry_id 指针 nil/非 nil 还原、
  action 筛选、`limit/offset` 分页。
- `TestListLimitClamp`：limit 0/-1/201/999 均按 50 处理。
- `TestCount`：全量计数、action 筛选计数、无匹配返回 0。
