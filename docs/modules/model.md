# 领域模型（internal/model）

## 职责

定义全仓共享的领域类型（Entry/Field/Template/APIToken/AuditLog/User）与审计动作
常量，承载两类契约：`Entry.Validate` 的"规范化 + 校验合一"入口契约（TrimSpace、
默认值、字段查重），以及 JSON 标签决定的前后端 wire 契约（`value`/`tags`/`token`
的 omitempty、遮蔽占位 `***`）。零 IO、仅依赖 `strings`，是分层中最底层的业务词汇表。

## 关键文件与入口

| 文件 | 关键函数/类型 | 说明 |
|---|---|---|
| model.go | `Field`、`Entry`、`Template`、`APIToken`、`AuditLog`、`User` | 六个核心领域类型 |
| model.go | `FieldTypeText/URL/Multiline`、`MaskedValue` | 字段类型枚举与遮蔽占位常量 `"***"` |
| model.go | `Action*` 常量（20 个） | 审计动作词表：login/entry_reveal/field_reveal/token_*/key_rotate/totp_gen/export/import 等 |
| model.go | `Entry.Validate`、`Entry.MaskSecrets`、`Entry.FieldByKey` | Entry 的三个行为方法 |
| model.go | `Field.ValidType`、`Field.Normalize`、`normalizeTags`（内部） | 字段级规范化与类型白名单 |
| model.go | `APIToken.HasScope`、`APIToken.Effective` | 令牌 scope 判定 |

## 对外接口

- `Entry.Validate() string`：先规范化后校验，返回 `""` 即通过，非空为可直接展示的
  中文错误文案。api 层创建/更新必须在通过校验后才把 Entry 交给 store：
  - 原地 TrimSpace 标题/分类/描述；`normalizeTags` 去空、trim、去重、保序；
  - 标题空 → "标题不能为空"；分类空 → 默认 `"misc"`；
  - 每个字段 `Normalize` 后要求：名称非空（"存在未填写名称的字段"）、trim 后
    不重复（"字段名重复：xxx"）、类型在白名单（"字段类型无效：xxx"）。
- `Field.Value` 三态契约（源码注释明示，贯穿三层）：
  - 存储层：`IsSecret` 时为密文（`enc:v1:base64`）；
  - 服务层：store 解密后为明文；
  - API 输出：被 `MaskSecrets` 换成 `***`。
  - 空值因 `json:"value,omitempty"` 不出现在 JSON——这是模板下发与遮蔽形态的
    wire 契约，前端以 `"value"` 缺失表示"无值"。
- `Entry.MaskSecrets()`：原地遍历，仅 `IsSecret` 字段值替换为 `MaskedValue`，
  非敏感值原样保留；是出 API 前的最后一道遮蔽。
- `Entry.FieldByKey(key) *Field`：按字段名精确查找（不做 trim/大小写宽容），
  未命中返回 nil；reveal 路径按它定位目标字段。
- `APIToken`：库中只存哈希，`Token` 字段 `omitempty`——仅创建响应携带明文；
  `Effective()` 定义为 `RevokedAt == nil`（未吊销即有效，过期判断归调用方）；
  `HasScope(s)` 线性查 `Scopes`。
- `AuditLog.ActorType` 词表 `user | token | system`；`EntryID *int64` 用 nil 表示
  与条目无关的动作。
- JSON 标签即前后端契约：`ai_visible`、`is_secret`、`created_at` 等 snake_case
  字段名不得改动，前端 `web/src` 按此解析。

## 依赖关系

- 依赖：仅标准库 `strings`，无内部依赖。
- 被依赖：`internal/{api,aiapi,aichat,audit,db,store,importer,auth,templates}`
  全部业务包，是依赖图的根。
- 数据流一句话：api/cli/importer 构造 Entry → `Validate` → store 按
  `Field.IsSecret` 决定加解密 → 出库后 api 层 `MaskSecrets` 再返回前端。

## 安全与不变量

- 敏感字段出 API 必经 `MaskSecrets`（或 reveal 路径的显式豁免并记审计）——
  `MaskedValue = "***"` 是前端识别"此处有密文"的哨兵，更新时 store 以它还原旧值。
- `Validate` 是字段名查重的唯一关卡，且在 trim 之后比较（`" a"` 与 `"a "` 判重），
  防止存储层 JSON 字段键冲突。
- 类型白名单封闭（text/url/multiline/空），空类型经 `Normalize` 收敛为 `text`，
  杜绝任意字符串入库当类型。
- `User` 类型不含密码哈希字段——密码哈希只在 `auth` 包内部流转，model 层不为其留位。
- 审计动作常量是 `audit_logs.action` 列的词表，api/aiapi 写审计必须取自此处
  常量而非手拼字符串。

## 测试锚点

`model_test.go`：

- `TestValidate` 锚定规范化（trim/默认分类/tags 去重）与四类失败分支 + trim 后查重；
- `TestEntryFieldByKeyAndMask` 锚定遮蔽只作用于 `IsSecret`、`FieldByKey` 命中/未命中；
- `TestEntryJSONShape` 锚定 `value`/`tags` 的 omitempty wire 契约；
- `TestFieldValidType`/`TestFieldNormalize`/`TestAPITokenHelpers` 锚定类型白名单、
  默认类型收敛与令牌 scope/吊销判定。
