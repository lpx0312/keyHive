# 字段加密（internal/crypto）

## 职责

实现敏感字段的 AES-256-GCM 加解密（`enc:v1:` 前缀 + base64(nonce+密文) 格式）、
主密钥的三来源加载与自动生成（env > keyfile > 数据目录文件）、启动自检用的
`key_check` 校验值生成与验证；空/纯空白密钥文件被拒绝启动，防止主密钥退化为
公开常量。

## 关键文件与入口

| 文件 | 关键函数/类型 | 说明 |
|---|---|---|
| crypto.go | `Cipher`、`New(masterKey []byte)` | 基于 AES-256-GCM 的加解密器；密钥必须恰 32 字节 |
| crypto.go | `Encrypt`/`Decrypt`/`IsEncrypted` | 密文格式 `enc:v1:base64(nonce+sealed)`；无前缀输入解密时原样透传 |
| crypto.go | `KeyCheck`/`VerifyKeyCheck` | 对固定明文 `keyhive-key-check-v1` 加密存 `settings` 表，启动时验证密钥匹配 |
| crypto.go | `KeySource`、`LoadMasterKey(dataDir)` | 三来源优先级加载主密钥，返回密钥与来源描述 |
| crypto.go | `readKeyFile`（内部）、`deriveKey`（内部）、`DeriveKeyText` | 文件读取（拒绝空内容）；SHA-256 派生 32 字节；后者是导出版供 rotate-key 同源派生 |

## 对外接口

- `New(masterKey)`：密钥长度必须恰 32 字节（AES-256），否则报错——调用方统一喂
  `deriveKey`/`DeriveKeyText` 的产物。
- `Encrypt(plain string) (string, error)`
  - 每次随机 nonce（`crypto/rand`，长度取 `aead.NonceSize()`），同明文两次加密
    结果不同；
  - 输出恒为 `enc:v1:` + base64(nonce + GCM sealed)。
- `Decrypt(s string) (string, error)`
  - 无前缀原样返回（视为明文，兼容手工导入的存量数据）；
  - 带前缀但坏 base64、载荷短于 nonce+1、被篡改均报错（GCM 认证失败包装为
    "解密失败（主密钥不匹配或数据损坏）"）；
  - 非本版本前缀（如 `enc:v2:whatever`）因前缀不匹配而走透传分支原样返回；
  - 异常输入绝不返回半解密结果。
- `IsEncrypted(s) bool`：严格前缀匹配、大小写敏感——`ENC:V1:`/`xenc:v1:`/
  带前导空格/`enc:v2:` 均判为否。
- `KeyCheck() string` / `VerifyKeyCheck(stored string) bool`
  - `KeyCheck` 即 `Encrypt(keyCheckPlain)`，密文形态入库（`settings.key_check`）；
  - `VerifyKeyCheck` 对空串、普通明文、坏 base64、长度不足、被篡改密文、加密了
    其他明文、前缀前有空格等形态一律返回 false。
- `LoadMasterKey(dataDir) (*KeySource, error)`
  - 优先级：`KEYHIVE_MASTER_KEY` env > `KEYHIVE_KEYFILE` 文件 > `dataDir/master.key`；
    env 与 keyfile 纯空白视同未设置；
  - 第三来源不存在时自动生成 32 字节随机数，以 base64 文本 + 换行写盘
    （目录 0700、文件 0600）；
  - 返回 `KeySource{Key, FromEnv, Path, Desc}`：`FromEnv=true` 时服务无法代写
    新密钥（rotate-key 依赖此判定），`Path` 是文件来源的有效路径，`Desc` 供
    启动日志打印。
- `DeriveKeyText(s) []byte`：与内部 `deriveKey` 同一函数的导出形态，rotate-key
  生成新密钥时保持同源派生（任意输入 SHA-256 → 32 字节，稳定可复现）。

## 依赖关系

- 依赖：仅标准库（crypto/aes、crypto/cipher、crypto/rand、crypto/sha256、
  encoding/base64、os、path/filepath、strings），零内部依赖。
- 被依赖：`cmd/keyhive`（buildServer 加载主密钥、ensureKeyCheck 校验）、
  `internal/store`（字段加解密编排）、`internal/api/rotate`（轮换重加密与落新密钥）。
- 数据流一句话：`LoadMasterKey` → `New` → `Cipher` 注入 `store.Store` →
  敏感字段出入库加解密；`KeyCheck/VerifyKeyCheck` 只在启动与轮换路径使用。

## 安全与不变量

- 空密钥文件必须拒绝：`readKeyFile` 对 TrimSpace 后为空的文件返回 error 拒绝
  启动——否则主密钥退化为公开常量 `SHA-256("")`，加密形同虚设（误创建的
  0 字节文件、写入截断是该缺陷的现实来源，`crypto_bug_test.go` 复现）。
- 主密钥与数据库文件分离存储；密钥正确性由 `key_check` 密文闭环验证（能解开
  且明文等于固定串），不匹配即拒启。
- 加密必为 GCM 认证加密：nonce 随机、密文防篡改；确定性输出视为缺陷
  （`TestEncryptNotDeterministic` 锚定）。
- 无前缀即明文的透传语义是读取侧兼容；写入侧（store 的 `encryptFieldsWith`）
  明确不做前缀跳过，防"用户明文恰好以 `enc:v1:` 开头"绕过加密入库。
- 主密钥明文只存在于进程内存与 0600 权限的密钥文件中，不进数据库、不进日志
  （日志只打 `keySrc.Desc` 来源描述）。

## 测试锚点

- `crypto_test.go`：往返一致、非确定性、错钥失败、明文透传、key_check 闭环、
  master.key 自动生成与复读一致、env 优先、`DeriveKeyText` 稳定。
- `crypto_edge_test.go`：`New` 长度校验、坏 base64/过短/篡改密文报错、
  `KEYHIVE_KEYFILE` 生效与缺失报错、master.key 被同名目录遮蔽报错。
- `crypto_blind_test.go`：三来源优先级矩阵与空白 env 跳过、keyfile 指向目录、
  数据目录不可写、key_check 各畸形形态拒绝、`enc:v2:` 等畸形输入、MB 级与含
  NUL 明文往返、`IsEncrypted` 边界。
- `crypto_bug_test.go`：`TestBug_EmptyKeyfileRejected` 针对空 master.key 的缺陷
  复现测试（修复后转绿）。
