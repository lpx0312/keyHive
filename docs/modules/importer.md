# importer 模块（internal/importer）

## 职责

外部凭据迁移的解析层：把 Bitwarden / Chrome 密码管理器导出的 CSV 与 keyHive 原生导出 JSON 解析为待导入条目，供 CLI `keyhive import` 与 Web `POST /import` 两个入口共用（同一份解析代码，dry-run 预览与正式写入路径一致）。只负责“解析与字段映射”，不直接写库——入库统一走 `store.CreateEntry`，复用校验+加密+审计链路。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/importer/importer.go` | `Entry`、`Parse`（按 format 分派 CSV）、`parseCSV`（通用表头→行映射）、`ToEntryJSON`（字段映射为条目对象） |
| `internal/importer/keyhive.go` | `ParseKeyhive`（原生导出 JSON 恢复导入，含遮蔽拒绝）、`KeyhiveUsername`（Web 预览展示用） |
| `internal/importer/importer_test.go`、`keyhive_test.go` | 格式矩阵与字段映射测试 |
| `internal/importer/edge_test.go`、`bug_test.go` | BOM/CRLF/重复列名边界、畸形引号、notes 敏感位回归 |

> 本包零 IO、零 DB：所有函数纯解析/纯映射，天然可测且两入口（CLI/Web）行为一致。

## 对外接口

- `type Entry struct { Title, URL, Username, Password, TOTP, Notes string }` — CSV 解析后的中间形态（keyhive 格式直接产出 `model.Entry`，不经它）。
- `Parse(format string, data []byte) ([]Entry, error)`：
  - format 仅接受 `bitwarden` / `chrome`（大小写不敏感，其他值报「未知格式」）；
  - 自动剥 UTF-8 BOM（`0xEF 0xBB 0xBF`）；
  - Bitwarden 列映射：`name / login_uri / login_username / login_password / login_totp / notes`，name 空时回退 login_uri，三者全空的行跳过；
  - Chrome 列映射：`name / url / username / password`，name 与 password 全空的行跳过。
- `ToEntryJSON(e Entry, source string) map[string]any` — 映射为可直接 `POST /entries` 的对象，字段映射表：

| 源字段 | 目标字段 key | 类型 | is_secret | 备注 |
|---|---|---|---|---|
| URL / url | `url` | text | 否 | 空值跳过 |
| Username / username | `username` | text | 否 | — |
| Password / password | `password` | text | 是 | — |
| login_totp（仅 Bitwarden） | `totp_secret` | text | 是 | `totp.ParseOTAuth` 提取纯密钥 |
| notes（仅 Bitwarden） | `notes` | multiline | 是 | 有值时整条 `ai_visible=false` |

  - `category="web_account"`（与内置模板字段名对齐）、description 记录「从 <source> CSV 导入 @ <日期>」；返回值经 JSON 序列化后由 CLI/Web 解码为 `model.Entry` 落库。
- `ParseKeyhive(data []byte) ([]model.Entry, error)` — 解析 keyHive 明文导出的条目数组（`GET /api/v1/export` 的输出）；`id/created_at/updated_at` 元数据清零，导入时重新生成；空文件、空数组、非条目数组结构均报错。
- `KeyhiveUsername(e *model.Entry) string` — Web 预览展示用，取 username 字段（无则空串）。

## 依赖关系

- 依赖：`internal/totp`（`ParseOTAuth`）、`internal/model`（Entry/Field 与 `MaskedValue`）。
- 被依赖：`internal/cli`（cmdImport / importKeyhive）、`internal/api`（importCSV / importKeyhive）。
- 不依赖 store/DB——加密与审计发生在入库端点一侧。

## 安全与不变量

1. **CSV 严格引号**：`csv.Reader` 保持 `LazyQuotes=false`——未闭合引号等畸形 CSV 直接报错拒绝，而不是静默吞行/产出脏数据（密码迁移是“迁移后即删源”的高信任操作，宁可失败不可错导，`TestBug_MalformedCSVRejected`）；`FieldsPerRecord=-1` 仅容忍各行列数不齐，表头读取失败也报错。
2. **Bitwarden notes 按敏感导入且默认 AI 不可见**：notes 是自由文本，常含恢复码/密保答案等高敏内容——映射为 `is_secret=true` 的 multiline 字段（走遮蔽与 reveal 审计管控），且含 notes 的条目 `ai_visible=false`（明文不直达 AI 上下文），用户确认安全后到 Web UI 手动打开（`TestBug_BitwardenNotesAreSecret`）。
3. **遮蔽导出拒绝恢复导入**：`ParseKeyhive` 发现任一 `IsSecret` 字段值为 `model.MaskedValue`（`***`）即整体报错并指明条目与字段名——遮蔽版不含真实密钥，不能用于恢复（`TestParseKeyhiveRejectsMasked` / `TestParseKeyhiveRejectsPartiallyMasked`）。
4. **敏感位从不缺失**：password / totp_secret / notes 一律 `is_secret=true`；Chrome 无 notes/TOTP 列则不生成对应字段。
5. **dry-run 同路径**：CLI `--dry-run` 与 Web `dry_run` 走同一解析函数后仅跳过写入，保证“预览所见 = 导入所得”：
   - CLI：打印解析条数 + 前 5 条（title + 截断到 20 字符的用户名）；
   - Web：返回 `{count, preview}`，preview ≤20 条（keyhive 格式含 `KeyhiveUsername`）；
   - 正式导入逐条 `CreateEntry`，失败条目收集进 fail 列表不中断整体（api 端 importCSV / importKeyhive）。
6. 解析在客户端/服务端各执行一次但代码同一份；重复列名时后者覆盖前者（`TestParseCSVDuplicateColumnNames`），BOM/CRLF/引号字段均正确处理。

## 测试锚点

`TestBug_BitwardenNotesAreSecret`、`TestBug_MalformedCSVRejected`、`TestParseCSVBOMCRLFQuotedFields`、`TestParseCSVChromeCRLFAndBlankLines`、`TestParseCSVDuplicateColumnNames`、`TestParseBitwardenCSV` / `TestParseChromeCSV`、`TestParseUnknownFormat`、`TestParseKeyhiveRejectsMasked` / `TestParseKeyhiveRejectsPartiallyMasked`、`TestParseKeyhiveBOMAndEmptySecret`、`TestToEntryJSON` / `TestToEntryJSONSkipsEmpty`、`TestKeyhiveUsername`。
