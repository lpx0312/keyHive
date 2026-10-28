# 加密与 TOTP（crypto/totp）代码审查报告

> 审查日期：2026-10-28 · 审查方式：只读代码审查（逐文件完整通读 + 交叉引用调用方验证）

## 概览

本次审查覆盖 `internal/crypto/`（crypto.go、crypto_test.go、crypto_edge_test.go）与 `internal/totp/`（totp.go、totp_test.go）共 5 个文件，并交叉核对了 store / api / main / db 中的调用点以验证结论。总体评价：两个包的实现小而正确——AES-256-GCM 使用随机 nonce、密文格式版本化、TOTP 严格符合 RFC 6238/4226 且官方测试向量全过，全仓无 `math/rand` 误用，`enc:v1:` 字符串密文进 SQLite TEXT 列的编码全程一致。主要风险不在密码学原语本身，而在主密钥**输入校验的健壮性**（空密钥文件可产生可预测主密钥）与**防错机制可被绕过**（key_check 明文透传）；无 P0 阻断级问题。

## 发现清单

### P1 严重

**[P1] 空密钥文件被静默接受，主密钥退化为公开可推导常量**（internal/crypto/crypto.go:134-140）

`readKeyFile` 对文件内容 `TrimSpace` 后直接 `deriveKey`，无任何长度/内容校验。当 `data/master.key` 或 `KEYHIVE_KEYFILE` 指向的文件**存在但为空**（运维提前 `touch` 占位、写入被截断、同步工具产生空文件等常见情形）时：

- `LoadMasterKey` 走"文件存在"分支（crypto.go:113-118）直接读取，不会触发重新生成；
- `deriveKey("")` = SHA-256("") = `e3b0c442...b7852b855`，是一个任何人都能算出的**公开常量**；
- 若是全新数据库，`ensureKeyCheck`（cmd/keyhive/main.go:149-154）会用这个可预测密钥正常写入 key_check 并完成初始化，**全程无任何告警**。

影响：加密完全失效——任何拿到 SQLite 文件的人可立即解密全部敏感字段，等同于明文存储。这是"密钥与库分离"这一核心安全承诺的静默失守。

修复建议：`readKeyFile` 对 TrimSpace 后为空或过短（如 < 16 字符）的内容返回明确错误；或 `LoadMasterKey` 对空的 `master.key` 视为损坏、拒绝启动（不自动覆盖，避免误删有效凭据的歧义）。

### P2 一般

**[P2] KEYHIVE_MASTER_KEY 弱口令经单次 SHA-256 派生，库泄露后可高速离线爆破**（internal/crypto/crypto.go:102-104, 143-146）

`deriveKey` 对任意输入只做一次 SHA-256，无迭代、无内存困难度。文件路径不受影响（自动生成的 master.key 是 32 字节 crypto/rand 随机值的 base64，256 位熵）；但 `KEYHIVE_MASTER_KEY` 按"任意口令"设计（注释明示），用户若设置弱口令，攻击者拿到数据库后可用 `settings.key_check` 密文作为离线验证 oracle，以 GPU 每秒数十亿次的速度猜测。argon2id 已是仓库依赖（internal/auth/password.go），修复成本低。

修复建议：检测 env 值不属于高熵格式（64 位 hex 或 43+ 字符 base64）时改用 argon2id/scrypt 派生；为兼容存量数据可引入 `enc:v2:` 前缀标识新派生方式，或在文档中明确警示 env 密钥必须使用强随机值。

**[P2] key_check 校验可被明文替换绕过**（internal/crypto/crypto.go:52-53, 84-87）

`VerifyKeyCheck` 通过 `Decrypt(stored)` 实现，而 `Decrypt` 对无 `enc:v1:` 前缀的输入**原样透传**（crypto.go:52-53）。若 `settings.key_check` 被写入明文常量 `"keyhive-key-check-v1"`（该常量在源码中公开），则**任意主密钥都能通过校验**。前提是攻击者可写 settings 表，虽不直接泄露明文，但 key_check 的唯一职责——启动时检测密钥不匹配——被静默禁用，服务以错误密钥启动后仅在字段级逐条报错，误导排障方向。正常写入路径必然产生带前缀密文，因此该形态只能是篡改产物。

修复建议：`VerifyKeyCheck` 先检查 `IsEncrypted(stored)`，非密文形态一律返回 false（或单独报"key_check 已损坏"），关闭透传通道。

### P3 建议

**[P3] GCM 加密未使用 AAD 绑定上下文，密文可在条目/字段间移植**（internal/crypto/crypto.go:46）

`Seal(nil, nonce, plain, nil)` 的 AAD 恒为 nil，密文未绑定 entry id、field key 或字段用途。持有数据库写权限者可将条目 A 的密文整体复制到条目 B（fields 为整列 JSON 密文），解密不会报任何错误（cut-and-paste）。属纵深防御缺失，需与其他越权/注入问题叠加才可利用，故列建议级。

修复建议：为 Cipher 增加带上下文的 Encrypt/Decrypt 变体，AAD 取 `entry:<id>|field:<key>`，由 store 层调用；需先评估与现有透传/轮换逻辑的兼容。

**[P3] 主密钥材料长期驻留内存，无 zeroize**（internal/crypto/crypto.go:20-22, 90-95）

`Cipher.aead` 与 `KeySource.Key` 常驻至进程退出，core dump / swap 场景可能残留密钥材料。客观说明局限：Go 标准库 `aes.NewCipher` 会将密钥展开进内部密钥调度表且无法清零，即使清掉 `Key` 也只是部分缓解。建议仅做低成本加固：rotate 完成替换 `s.Cipher` 后尽力清零旧 `KeySource.Key`。

**[P3] master.key 权限仅在创建时设置，存在时不复查**（internal/crypto/crypto.go:128）

新建时 0600 + 目录 0700 正确；但文件已存在时不校验权限——被放宽到 0644 后静默接受。另注：Windows 上 Unix 权限位对 ACL 无实际约束力（本项目主要部署形态之一）。建议启动时 `Stat` 检查 `mode.Perm()`，非 0600 输出告警日志（非阻断）。

## 做得好的地方

1. **nonce 生成正确**：每次加密用 `crypto/rand` 现生成 12 字节随机 nonce（crypto.go:42-44），无计数器复用或跨调用复用风险；`Encrypt` 非确定性有专门测试保障（crypto_test.go:36-43），且 `append(nonce, sealed...)` 因 nonce cap==len 无切片别名隐患。
2. **GCM 用法与密文格式规范**：认证加密保证完整性与机密性，解密失败不构成 padding-oracle 类侧信道；`enc:v1:` 前缀实现了密文格式版本化，进 SQLite 统一为 TEXT（fields JSON / settings.value），全程编码一致，并为将来派生方式升级（如 v2）预留了空间。
3. **TOTP 严格符合标准且兼容主流验证器**：HMAC-SHA1、30s 窗口、6 位码、RFC 4226 动态截断逐位正确，RFC 6238 附录 B 官方向量全过（totp_test.go:10-32）；base32 解码的容错（大小写、空格分组、多余 padding）与 Google Authenticator 习惯完全兼容。
4. **随机数纪律好**：全仓（internal/ + cmd/）无一处 `math/rand`；master.key 自动生成（crypto.go:120-124）与 rotate 新密钥（api/rotate.go:20-27）均为 32 字节 crypto/rand，且 rotate 与 LoadMasterKey 保持同一派生惯例，轮换有事务回滚保护。
5. **key_check 的密钥匹配检测隐式依赖 GCM tag 验证**：错误密钥在 `aead.Open` 的认证环节即失败（通过 tag 的概率约 2^-128），之后的明文常量比对不构成有意义的时序侧信道，无需额外恒定时间比较。

## 测试盲区

- **空/超短 master.key、空 KEYHIVE_KEYFILE 场景无测试**（即 P1 场景；现有测试均为有效内容文件）。
- **key_check 被替换为明文常量的绕过路径无测试**（即 P2-2 场景，透传测试只验证了"普通明文原样返回"）。
- **KEYHIVE_MASTER_KEY 为纯空白字符**时的优先级行为（TrimSpace→空→跳过 env）未直接覆盖。
- **大字段（MB 级明文）加解密**的性能与正确性无测试。
- **decodeSecret 对非半角空白**（如全角空格 U+3000）的行为：未被过滤、报"非有效 base32"，错误提示与根因偏离，属可用性盲区。
- **master.key 权限被放宽（0644）后启动**无检测无测试（对应 P3-3）。
- **TOTP 无验证/skew 路径**：本应用的 TOTP 是代管第三方动态码（webui/cli 生成展示），不承担自身登录 2FA，故 skew、重放防护、恒定时间比较暂不适用；若未来引入登录 2FA，需按 RFC 6238 §5.2 补 skew 窗口与重放测试。
