# 数据库基础（internal/db）

## 职责

负责 SQLite 的打开与连接配置（WAL、外键、忙等待、单连接串行化）、建表迁移
（`CREATE TABLE IF NOT EXISTS` 全量表结构 + 旧库幂等补列 + 管理员兜底提升）与
首次启动种子（admin 账号 + 内置模板），是所有表结构的唯一定义点；上层包拿到的
`*sql.DB` 均已带全量 schema。

## 关键文件与入口

| 文件 | 关键函数/类型 | 说明 |
|---|---|---|
| db.go | `Open(path string) (*sql.DB, error)` | MkdirAll(0700) → 拼 DSN 打开 → 建表迁移 → seed；迁移失败关闭连接并包错"数据库迁移失败" |
| db.go | `migrate(d)`（内部） | 执行全部 `CREATE TABLE/INDEX IF NOT EXISTS`，再走补列与 seed |
| db.go | `migrateUsersColumns(d)`（内部） | `pragma table_info(users)` 探测后幂等补 `is_admin`/`disabled` 列；无管理员时兜底提升 |
| db.go | `migrateEntriesColumns(d)`（内部） | 同法补 `entries.tags` 列（v1.3.1 新增），存量行默认 `'[]'` |
| db.go | `seed(d)`（内部） | 以 `settings.seeded` 标记保证只跑一次：建 admin（随机密码）+ 写入内置模板 |
| util.go | `nowUTC()`、`marshalFields`（内部） | RFC3339 UTC 时间戳与 `[]model.Field` JSON 序列化小工具 |

## 对外接口

- `Open` 是本包唯一导出函数，契约是"打开即迁移完"：
  - 返回的库已含 7 张表——`users`、`sessions`、`api_tokens`、`entries`、
    `category_templates`、`audit_logs`、`settings`——及两个索引
    `idx_entries_category`、`idx_audit_created(created_at DESC)`。
  - 路径非法、父目录不可创建、迁移 SQL 失败时返回 error；支持 `":memory:"`。
  - 调用方负责 `Close`（`cmd/keyhive.buildServer` 出错路径与成功路径均已处理）。
- 连接配置固定四条 PRAGMA：`journal_mode(WAL)`、`foreign_keys(1)`、
  `busy_timeout(5000)`、`synchronous(NORMAL)`。
- `SetMaxOpenConns(1)` 的原因：SQLite 并发写有限，单连接串行化所有语句以避免
  `database is locked`；代价是全库读写共用一个连接，吞吐靠 WAL 下读多写少的
  负载特征保障。
- 表结构要点（供其他包对照）：
  - `entries.fields`/`tags` 存 JSON 文本，默认 `'[]'`；`ai_visible` 默认 1。
  - `api_tokens.token_hash` UNIQUE；`scopes` 默认 `'["read"]'`。
  - `sessions.user_id` 外键引用 `users(id) ON DELETE CASCADE`（依赖 foreign_keys PRAGMA）。
  - 时间列统一 TEXT 存 RFC3339 UTC 字符串。

## 依赖关系

- 依赖：`internal/auth`（seed 用 `RandomToken` 生成初始密码、`HashPassword` 落哈希）、
  `internal/templates`（`Builtin()` 内置模板）、`internal/model`（util.go 字段序列化），
  驱动为 modernc.org/sqlite（纯 Go，无 CGO）。
- 被依赖：`cmd/keyhive` 的 `buildServer` 与 `internal/kdbtest` 测试夹具直接调用；
  其余业务包不 import 本包，而是接收注入的 `*sql.DB`——schema 知识集中于此，
  SQL 语句分散在各业务包。

## 安全与不变量

- 首启 admin 密码是 `auth.RandomToken()` 随机值，库内只存 argon2id 哈希，明文仅
  启动日志打印一次并提示立即修改，seed 之后不再显示。
- `seed` 幂等性由 `settings.seeded` 标记保证：二次 `Open` 不重建 admin、不重复插
  模板（模板插入另用 `INSERT OR IGNORE` 双保险）。
- 迁移兜底不变量：任何库至少有一个管理员——无 `is_admin=1` 用户时优先提升用户名
  `admin`（`COLLATE NOCASE`），否则提升 `MIN(id)` 最早用户；空 users 表跳过不报错。
- 补列逻辑必须幂等（`ALTER TABLE ADD COLUMN` 不支持 IF NOT EXISTS，先查
  `pragma table_info` 再补），存量行靠 `NOT NULL DEFAULT` 填默认值。
- 目录以 0700 创建，库文件所在数据目录权限由调用方（部署层）约束；本包不写
  任何明文密钥进库。

## 测试锚点

- `db_test.go` 的 `TestMigrateEntriesTags`：模拟 v1.3.0 无 tags 列的旧库，`Open`
  后补列且存量行默认 `'[]'`。
- `db_extra_test.go`：
  - `TestOpenMemorySeed`/`TestSeedIdempotent` 锚定种子内容与幂等；
  - `TestMigrateUsersColumns`/`TestMigratePreferAdminNamedUser`/`TestMigrateEmptyUsers`
    锚定管理员兜底三分支（提升最早用户 / 优先 admin 同名 / 空表放行）；
  - `TestOpenBadPath`/`TestOpenMigrateFailure` 锚定非法路径与坏表结构
    （索引建在缺失列上）的失败路径；`TestMarshalFields` 锚定工具函数行为。
