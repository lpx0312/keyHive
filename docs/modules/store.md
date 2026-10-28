# store 模块（internal/store）

## 职责

条目（entries）与模板（category_templates）两表的全部数据访问层：负责 fields JSON 的序列化/反序列化、敏感字段的加解密编排（写前加密、读后解密）、`***` 遮蔽哨兵的还原、坏行隔离的列表降级，以及主密钥轮换时对全库条目与 settings 表密文的原子重加密。它是 api、aiapi、kdbtest 三个上层共用的唯一持久化入口，自身不感知 HTTP。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/store/store.go` | 全部实现：`Store` 结构体、条目/模板 CRUD、加解密编排、`RotateAllEntries` |
| `internal/store/store_test.go` | CRUD、加密静态检查、轮换主路径测试 |
| `internal/store/store_extra_test.go` | 哨兵保留、分类过滤、Unicode 搜索等补充测试 |
| `internal/store/store_bug_test.go` | 安全修复回归（坏行隔离、enc 前缀明文、settings 轮换遗漏） |
| `internal/store/store_edge_test.go` | 空库轮换、关闭库、错误路径等边界 |

## 对外接口

- `type Store struct`：
  - 导出字段 `DB *sql.DB`、`Cipher *crypto.Cipher`、`DataDir / KeyFilePath string`、`KeyFromEnv bool`——后三者供 rotate 决定新密钥持久化位置（写文件还是返回给管理员改 env）；
  - `Cipher` 由内部 `sync.RWMutex` 保护，`cipher()` 并发安全读取（rotate 期间读写不撕裂）。
- `NewForTest(db) (*Store, error)`：测试夹具，随机 32 字节主密钥构造（kdbtest 间接使用）。
- 条目操作：
  - `CreateEntry(*model.Entry) error`：先 `Validate`，敏感字段加密后落库，回填 ID 与 created/updated 时间；
  - `UpdateEntry(id, *Entry) error`：值为 `model.MaskedValue`（`***`）的敏感字段先从旧条目回填原值明文，再整体重加密更新；
  - `GetEntry(id) (*Entry, error)`：返回已解密明文，调用方负责输出前遮蔽；
  - `DeleteEntry(id)`；`Categories() ([]string, error)`；
  - `ListEntries(q, category) ([]Entry, error)`：title/description/category/fields/tags 五列 LIKE 模糊 + category 精确，`ORDER BY updated_at DESC`。
- 模板操作：
  - `ListTemplates()` / `GetTemplate(id)`；
  - `SaveTemplate(*Template)`：新键时 category 空则取 `custom_<unix秒>`、group 空则「我的模板」，所有字段 `Value` 清空只留骨架，`builtin=0`；更新时 0 行受影响报「模板不存在」；
  - `DeleteTemplate(id)`：SQL 带 `AND builtin = 0`，内置模板物理不可删。
- `RotateAllEntries(newCipher) (int, error)`：见「安全与不变量」第 5 条。

## 依赖关系

- 依赖：`internal/crypto`（`Cipher.Encrypt/Decrypt/KeyCheck`）、`internal/model`（Entry/Field/Template 与 `Validate`）。
- 被依赖：`internal/api`、`internal/aiapi`、`internal/kdbtest`、`cmd/keyhive`（buildServer 组装 Store 并注入三个密钥位置字段）。
- 不依赖 HTTP 与 audit——审计由 api 层记，store 保持纯数据层。

## 安全与不变量

1. **敏感值一律加密，无前缀跳过**：`encryptFieldsWith` 对 `IsSecret && Value != ""` 的字段无条件 `Encrypt`，刻意不识别 `enc:v1:` 前缀做跳过——写入路径拿到的永远是待加密明文（创建/导入为用户明文、更新前 `***` 已还原为旧值明文、轮换前旧密文已解密），若跳过则用户明文恰以该前缀开头会原样入库成“伪密文”，读取时强制解密必败（`TestBug_PlaintextWithEncPrefixCorrupted`）。
2. **`***` 哨兵语义**：`UpdateEntry` 中敏感字段等于 `MaskedValue` 时经 `FieldByKey` 取回旧值明文再加密，前端未重输的密码不会被写成字面 `***`（`TestUpdateMaskedSentinelKeepsOldValue`）。
3. **坏行隔离**：`ListEntries` 中单条 scan/解密失败仅 `log.Printf` 记录条目 ID 后跳过（导出、AI 检索等所有列表路径共用此容错）；但命中的行**全部**损坏时整体报错（提示疑似主密钥不匹配或数据损坏），避免把系统性故障静默降级成空列表。`GetEntry` 始终严格报错（`scanEntry` 解密失败仍带出已扫描的 ID 供日志定位）。
4. **搜索不泄露密文**：fields 以 JSON 文本参与 LIKE，非敏感值（URL/用户名）明文可命中，敏感值是密文天然搜不到。
5. **轮换原子性**（`RotateAllEntries` 四步）：
   1. 旧钥读取全部 `entries.fields` 密文，逐条解密并用 `encryptFieldsWith(newCipher, ...)` 预计算新密文（任一条失败即中止，不动库）；
   2. settings 表中 `value LIKE 'enc:v1:%'` 且 `key != 'key_check'` 的密文（如 `ai_llm_api_key`）同样换钥预计算，`key_check` 除外——统一用新钥重新生成；
   3. 单事务重写全部 fields + settings + 新 `key_check`（`newCipher.KeyCheck()`），失败 `Rollback` 旧钥仍有效；
   4. **提交成功后**才加写锁替换内存 `Cipher`，返回重加密条数。
6. **模板不存值**：`SaveTemplate` 强制清空字段 Value；内置模板删除被 SQL 条件拒绝，但允许编辑（种子为 `INSERT OR IGNORE` 且仅首启执行，改动不会被升级覆盖）。
7. **空列表恒为 `[]` 而非 `null`**（`ListEntries`/`Categories`/`ListTemplates` 三处一致），前端 `entries.value=null` 会渲染崩溃。

## 测试锚点

- `TestSecretsEncryptedAtRest`：静态检查库内 fields JSON 不含明文密码；
- `TestBug_PlaintextWithEncPrefixCorrupted`、`TestBug_ListEntriesSurvivesBadRow`、`TestBug_RotateMissesSettingsSecret`（三条安全修复回归）；
- `TestRotateAllEntries`、`TestRotateEmpty`、`TestRotateErrorPaths`、`TestStoreCipherSwap`；
- `TestUpdateMaskedSentinelKeepsOldValue`、`TestTemplateBuiltinProtection`、`TestSearchUnicodeAndCaseBoundaries`、`TestValidateRejects`。
