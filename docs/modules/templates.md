# templates 模块（internal/templates）

## 职责

声明 keyHive 的全部内置录入模板（当前工作区共 **31 个**，分 8 组）：为每类常见凭据（云 AK/SK、镜像仓库、SSH、数据库、中间件、DevOps、证书、通用）预置一组字段骨架，且每个字段带写给 AI 看的中文 `description` 注释与 `is_secret` 敏感标记。本包是纯数据声明，唯一的函数 `Builtin()` 同时作为首次启动的种子数据来源（`internal/db.seed`）与前端模板选择器数据的源头；不含任何行为逻辑与 IO。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/templates/templates.go` | 8 个 `Group*` 分组常量、5 个字段构造辅助函数、`Builtin()` 全量模板定义 |
| `internal/templates/templates_test.go` | 完整性校验：分类唯一、字段合法、关键模板/分组在位、blank 特例 |
| `internal/templates/templates_extra_test.go` | 骨架可过 `Entry.Validate`、切片防篡改、blank 非敏感、字段辅助函数 |

## 对外接口

- `Builtin() []model.Template`：
  - 返回全部内置模板，每次调用新建切片（调用方修改不会污染后续调用，`TestBuiltinReturnsFreshSlice`）；
  - 每个模板 `Category` 全局唯一（建表 `UNIQUE` + 测试双重保证）、`Fields` 为骨架（`Value` 为空）、`Builtin` 字段由种子写入时置 1（本包不填）。
- 分组常量（8 个）：
  - `GroupCloud`（云平台）、`GroupRegistry`（容器镜像）、`GroupServer`（服务器/SSH）、`GroupDB`（数据库）、
  - `GroupMiddleware`（中间件/服务）、`GroupDevOps`、`GroupCert`（证书/授权）、`GroupGeneric`（通用）。
- 字段构造辅助（包内未导出，声明语义）：
  - `t(key, desc)`：非敏感 text；`tm(key, desc)`：非敏感 multiline；
  - `f(key, desc, secret)`：text + 指定敏感位；`fu(key, desc)`：URL；`fm(key, desc, secret)`：multiline + 敏感位。

字段设计惯例（AI 提示词与 aichat 工具 schema 同样遵循）：

- 字段名一律英文 `snake_case`（registry、access_key_id、private_key、totp_secret）；
- 同类字段跨模板同名（username/password/url/remark），保证 AI 检索与跨模板理解的一致性；
- 每个模板保留 `remark` 类说明字段（非敏感）记录用途与权限范围；
- 敏感大文本用 multiline（kubeconfig、私钥 PEM、notes），地址类用 url。

分组与模板清单（31 个）：

| 分组 | 数量 | category 清单 |
|---|---:|---|
| 云平台 | 6 | aliyun_aksk、huawei_aksk、tencent_aksk、aws_aksk、object_storage、k8s_cluster |
| 容器镜像 | 3 | docker_registry、harbor、huawei_swr |
| 服务器/SSH | 4 | ssh_server、ssh_key、server_panel、vpn |
| 数据库 | 5 | mysql、postgresql、redis、mongodb、mssql |
| 中间件/服务 | 4 | mq_service、smtp、alert_webhook、network_proxy |
| DevOps | 4 | git_service、jenkins_ci、monitoring、dns_provider |
| 证书/授权 | 2 | ssl_cert、software_license |
| 通用 | 3 | web_account、api_key、blank（空白自由定义） |

## 依赖关系

- 依赖：仅 `internal/model`（`Template`/`Field`），是业务层依赖最少的包。
- 被依赖：`internal/db`（`seed` 以 `INSERT OR IGNORE INTO category_templates ... builtin=1` 且仅首启 `settings.seeded` 不存在时执行一次）；`internal/api`（AI 聊天的 category 清单来自模板表）。
- 前端不直接 import 本包，经 `GET /templates` 消费种子后的表数据。

## 安全与不变量

1. **模板只是骨架**：所有字段 `Value` 为空；值仅在用户落条目时产生，敏感值由 store 层加密。模板表数据永不参与解密路径。
2. **`description` 是安全模型的一部分**：注释写给 AI 看（read scope 原样返回），用于支撑“字段名+注释足够完成任务时不取明文”的遮蔽优先纪律；注释描述用途与用法，永不包含真实值。
3. **敏感位预置准确**：AK/SK、密码、私钥（`private_key`）、kubeconfig、license key、webhook URL、加签 secret、PSK、`key_pem` 等一律 `IsSecret=true`；账号、地址、地域、端口默认非敏感；`blank` 模板的示例字段 `field1` 必须非敏感（`TestBlankTemplateFieldsNotSecret`）。
4. **builtin 保护在别处闭环**：本包只声明数据；删除保护由 `store.DeleteTemplate`（`WHERE builtin = 0`）与 api `deleteTemplate`（`Builtin` 时 400「内置模板不可删除」）实现；种子 `INSERT OR IGNORE` + 仅首启，内置模板的编辑改名不会被升级覆盖。
5. 字段类型只用 `text/url/multiline` 三种（`model.FieldType*`），保证骨架可过 `Entry.Validate` 的类型校验（`TestBuiltinSkeletonsPassEntryValidate`）。

## 测试锚点

- `TestBuiltinIntegrity`：分类不重复、每个模板有 category/name/字段、字段 key 与 description 齐全、关键模板（aliyun_aksk/docker_registry/ssh_server/blank）与分组在位；
- `TestBuiltinAllGroupsCovered`、`TestBuiltinSkeletonsPassEntryValidate`、`TestBuiltinReturnsFreshSlice`；
- `TestBlankTemplateFieldsNotSecret`（blank 示例字段非敏感）、`TestFieldHelpers`（五个构造函数的敏感位与类型正确）。
