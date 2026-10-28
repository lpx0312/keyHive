# 动态口令（internal/totp）

## 职责

实现 RFC 6238 时间型一次性密码（TOTP：HMAC-SHA1、30 秒窗口、6 位码），兼容
Google Authenticator / Bitwarden 等主流验证器的 base32 密钥与 `otpauth://` URI
输入。定位是"读码器"而非验证器：keyHive 只替用户生成条目上所存 TOTP 密钥的
当前动态码（每次生成由上层记 `totp_gen` 审计），不做登录双因素校验，也不做
动态码比对。

## 关键文件与入口

| 文件 | 关键函数/类型 | 说明 |
|---|---|---|
| totp.go | `Code(secret string, t time.Time) (string, error)` | 生成指定时刻的 6 位动态码（`%06d` 补零） |
| totp.go | `Current(secret string) (string, int, error)` | 当前动态码 + 本窗口剩余秒数（1~30） |
| totp.go | `decodeSecret(s)` | base32 容错解码（内部函数）：去空白/连字符/`=`、转大写、重整 padding |
| totp.go | `ParseOTAuth(v string) string` | 解析 `otpauth://totp/...?secret=XXX` 提取 secret，非该 URI 原样返回 |
| totp.go | `period = 30` | 窗口长度常量，与 RFC 及主流验证器一致 |

## 对外接口

- `Code(secret, t)`
  - `counter = t.Unix() / 30`（Go 整数除法向零截断，纪元前时刻与 T=0 同窗），
    8 字节大端 counter 进 HMAC-SHA1，按 RFC 4226 §5.3 动态截断取
    `& 0x7fffffff % 1000000` 后格式化为 6 位十进制字符串。
  - 密钥非法（空、非 base32 字母表字符、非半角字符）返回 error 不 panic。
  - 纯函数：同 secret 同时刻恒得同码，无任何内部状态。
- `Current(secret)`
  - 以 `time.Now()` 调 `Code`，返回 `(code, 剩余秒数, err)`；
    剩余秒数 = `period - int(now.Unix()%period)`，恒在 1~30 之间，
    前端据此渲染倒计时并在窗口切换后自动刷新取码。
- `ParseOTAuth(v)`
  - 输入 TrimSpace 后以 `otpauth://`（小写、大小写敏感）开头才走 `url.Parse`
    取 `secret` 查询参数（参数名大小写敏感）；否则视为纯密钥原样返回。
  - 调用方惯用组合：`totp.Current(totp.ParseOTAuth(field.Value))`——同一个
    字段既能存纯 base32 密钥，也能存 Bitwarden 导出的完整 otpauth URI。
  - 解析失败（如 URI 含控制字符）返回空串，由上层按"密钥为空"报错。
- `decodeSecret` 容错范围（供理解输入契约，非导出）
  - 过滤 `' '、'-'、'\n'、'\r'、'\t'、'='` 后转大写，长度非 8 的倍数补 `=`。
  - 全角空格（U+3000）/全角字母不在过滤范围，最终以 base32 解码错误暴露。

## 依赖关系

- 依赖：仅标准库（crypto/hmac、crypto/sha1、encoding/base32、net/url、time），零内部依赖。
- 被依赖三处：`internal/api/webui.go`（Web 取码接口 `POST /api/v1/entries/{id}/totp`）、
  `internal/cli/cli.go`（`keyhive totp` 命令）、`internal/importer/importer.go`
  （Bitwarden CSV 的 otpauth 列提取纯密钥后入库）。
- 数据流一句话：条目中 `IsSecret` 的 TOTP 字段密文出库解密 → `ParseOTAuth`
  归一 → `Code/Current` 出码 → 上层记 `model.ActionTOTPGen` 审计。

## 安全与不变量

- TOTP 密钥在条目中是普通敏感字段：必须 `IsSecret=true` 加密存储；出库解密只
  发生在取码与 reveal 路径，且每次取码记审计——密钥明文不落日志、不进 API 响应。
- 密钥清洗只认半角 base32 字母表，非半角字符一律报错而非静默忽略——防止全角
  字符混淆出"看似合法"的密钥，算出与验证器不一致的码。
- 本包不校验动态码（无 Verify 类函数）、不落任何状态；窗口参数固定 30s 与
  验证器生态对齐，不可配置，改了即与所有外部验证器失配。
- 前缀大小写敏感是刻意行为：`OTPAUTH://` 视为纯密钥而非 URI，避免宽容解析
  引入的歧义。

## 测试锚点

- `totp_test.go`：
  - `TestRFC6238Vectors` 用 RFC 6238 附录 B 官方向量（T=59→287082 等 6 组）
    锚定算法正确性；
  - `TestSecretBase32Tolerance`/`TestInvalidSecret` 锚定大小写/空格/padding
    容错与空、非法密钥报错；
  - `TestCurrent` 锚定码长 6 与剩余秒数 1~30；`TestParseOTAuth` 锚定 URI 提取
    与纯密钥透传。
- `totp_blind_test.go`（盲区补测）：窗口边界（T=29/30 切换、纪元前、远未来不溢出）、
  `\n`/`\r\n`/`\t`/连字符分隔变体、全角字符拒绝、base32 字母表外字符（0/1/8/9）
  拒绝、otpauth 大小写/缺参/控制字符边界。
