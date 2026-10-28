# CLI（cli）代码审查报告

> 审查对象：`internal/cli/`（cli.go、admincli.go、login.go、cli_test.go、cli_edge_test.go、cli_run_test.go、admincli_test.go），共 7 个文件、2103 行，全部通读。
> 审查方式：只读。为验证 CLI 侧行为的服务端语义，交叉查阅了 `internal/api/api.go`（login/getEntry/updateEntry）、`internal/store/store.go`（UpdateEntry 的 `***` 保留）、`internal/auth/password.go`（argon2id）、`internal/importer/`、`internal/totp/`、`cmd/keyhive/main.go`（dispatch）。

## 概览

CLI 包整体质量较高：子命令分发清晰、退出码语义（0 成功 / 1 运行错误 / 2 用法错误）基本一致、stdout（数据）与 stderr（日志/警告）分离纪律好、令牌与密码不进日志且 status 只显示"已配置/未配置"。edit 的"遮蔽 GET + `***` 回传 + 服务端保留原值"闭环经服务端代码核实成立，遮蔽版导入被正确拒绝，中文搜索词的 URL 编码有回归测试。

主要问题集中在 admin 通道的会话管理：`adminCall` 每次调用都重新登录，而 `import` 在循环里逐条调用 `AddEntry`，导致 N 条导入 = N 次 argon2id 验证 + N 个服务端会话 + N 条登录审计（P1）。另有三处 P2：`adminFlags` 忽略 flag 解析错误导致打错参数的 `rotate-key` 仍会继续执行、`login` 更新已有 config.json 时不会收紧其权限、admin 通道 15s 固定超时对 rotate-key/export 大库场景偏紧且超时误报为"服务未启动"。

## 发现清单

### P1（严重）

**[P1] import 循环每条记录都重新执行 admin 登录**（admincli.go:184-193、admincli.go:231-241、cli.go:127-129、admincli.go:43-48）

`cmdImport`/`importKeyhive` 在循环里逐条调 `AddEntry(cfg, user, pass, body)`，而 `AddEntry` → `adminCall` → `adminClient`，`adminClient`（admincli.go:22-40）每次都 POST `/api/v1/auth/login` 换新会话。服务端（internal/api/api.go:129-161）每次登录都要做一次 argon2id 密码验证（64MB/t=1，internal/auth/password.go:15-16）、`auth.CreateSession` 写一行会话、记一条 `ActionLogin` 审计。

影响：从 Bitwarden 迁移几百条时，导入 = 几百次 argon2id 验证（仅密码校验就数十秒）+ 2N 次额外 HTTP 往返；服务端 sessions 表被堆入 N 个有效会话凭据行；审计表被 N 条 login 记录稀释，`entry_create` 审计的信噪比大幅下降。中途任何一条触发登录失败即整批失败计数。

修复建议：导入命令登录一次并复用同一个 `*http.Client`（cookiejar 保存会话），循环内直接用该 client 发 POST；可将 `adminClient` 的产物下沉为循环外获取、循环内复用的调用形态。`add`（单条）保持现状即可。

### P2（一般偏严重）

**[P2] adminFlags 丢弃 flag 解析错误，打错参数的 rotate-key/edit 仍继续执行**（admincli.go:75）

`adminFlags` 中 `fs.Parse(args)` 的返回值被直接丢弃（`cmdEdit`、`cmdRotateKey` 均经此路径），而 `cmdAdd`（cli.go:483-485）、`cmdExport`（admincli.go:89）、`cmdImport`（admincli.go:142）都正确检查了 Parse 错误并返回 2。后果：`keyhive rotate-key --dry-run` 这类拼错/多余 flag 的命令，flag 包在 stderr 打印"flag provided but not defined"后程序**不退出**，继续用默认参数执行真正的 master key 轮换（或 edit 的 GET+PUT）。对一个"旧备份密钥全部失效"的危险操作，带错继续是不可接受的。

修复建议：`adminFlags` 返回 Parse 的 error（或返回退出码），`cmdEdit`/`cmdRotateKey` 在 Parse 失败时返回 2，与其余命令对齐。

**[P2] login 更新已有 config.json 时不会收紧文件权限，token 明文可能停留在 0644**（login.go:112）

`os.WriteFile(path, ..., 0o600)` 的 perm 仅在**新建**文件时生效（Go/OpenFile 语义），对已存在文件是原地截断写、权限不动。场景：用户先手工创建 `~/.keyhive/config.json`（默认 umask 022 → 0644）填入令牌，之后任何一次 `keyhive login` 合并写入后文件仍是 0644，`token_read`/`token_reveal` 明文可被同机其他用户读取。目录同理：`MkdirAll(dir, 0o700)`（login.go:103）不收紧已存在目录。（Windows 下 ACL 语义不同，此问题主要影响 Linux/macOS 部署。）

修复建议：写入成功后显式 `os.Chmod(path, 0o600)`（忽略 ENOTSUP 类错误），或采用"写临时文件 0600 + rename"方案（顺带解决下文 P3 的原子性问题）。

**[P2] admin 通道 15s 固定超时对 rotate-key/export 偏紧，且超时误报为"服务未启动"**（admincli.go:28、admincli.go:61-63、cli.go:94-98）

`http.Client{Timeout: 15s}` 覆盖从发起请求到读完响应体的全程。rotate-key 在服务端是同步重加密全部条目（解密+加密+逐条 UPDATE），export 是解密全库再传输，条目数大或磁盘慢时可能超过 15s。超时后 `client.Do` 的错误被统一折叠成 `ErrServiceDown`（cli.go:96-98），用户看到的是"服务未启动？docker start keyhive"——而服务端实际仍在执行且可能已成功；用户按提示"修复"后重试，会触发**第二次**密钥轮换，再次使刚生成的密钥失效，造成混乱。

修复建议：rotate-key（及 export）单独使用更长超时或请求级 context；`ErrServiceDown` 的判定区分 `net.Error.Timeout()`、connection refused、TLS 错误，超时文案应提示"服务可能仍在处理，先查状态再重试"而非引导重启。

### P3（建议）

**[P3] reveal 输出为包装 JSON，无法直接供 `docker login --password-stdin` 等管道消费**（cli.go:204）

reveal 的 stdout 是完整响应 JSON（`{"entry_title":...,"value":"..."}`），日志行都在 stderr（这点达标）。但管道场景下 `keyhive reveal 1 password | docker login --password-stdin` 会把整段 JSON 当作密码送入，认证必然失败。对比之下 totp（cli.go:411-412）做得正确：stdout 只有 6 位码，条目信息走 stderr。建议给 reveal 增加 `--value-only`（或 `--raw`）输出纯值。

**[P3] list/search 参数解析静默吞错**（cli.go:224-234、cli.go:249-258）

`parseListArgs` 只扫描到 `len(args)-1`：`--category`/`--stale` 作为最后一个参数缺值时静默忽略；多余的位置参数（如 `keyhive list foo`）静默丢弃；`--stale abc`、`--stale -5` 经 `atoi` 归零后**静默退化为不过滤**，用户以为过滤已生效。建议对无法识别/残缺的参数报用法错误（返回 2），`--stale` 非法值显式报错。

**[P3] totp 字段自动定位大小写不一致，reveal 响应解析错误被忽略**（cli.go:380-381、cli.go:404）

字段 key 的匹配用了 `strings.ToLower`（cli.go:380），description 的匹配却是大小写敏感的 `strings.Contains(f.Description, "TOTP")`（cli.go:381），description 写小写 "totp" 的条目匹配不到，报"未找到 TOTP 字段"误导用户。另外 cli.go:404 `json.Unmarshal(revData, &rev)` 错误被丢弃，响应异常时报的是"字段值不是有效的 TOTP 密钥"，方向错误。

**[P3] login 交互输入的 URL 不校验 scheme**（login.go:53）

flag 路径强制 `http://`/`https://` 前缀（login.go:43-46），交互 `promptLine` 却原样接受。输入 `192.168.0.10:8020` 这类无 scheme 地址会一路走到 `client.Do`，报"无法连接 + docker start 提示"，与真实原因（URL 格式）相去甚远。复用同一份前缀校验即可。

**[P3] login 写配置非原子、无并发保护**（login.go:112）

`os.WriteFile` 是原地截断写：写一半进程被杀/磁盘满会留下损坏的 config.json（下次 login 提示"手工修复或删除"，至少没有覆盖用户数据，这点好）。两个终端并发 `login` 会丢失后写者的更新（读-改-写无锁）。建议 temp 文件 + `os.Rename` 原子替换；并发窗口小，可接受仅注释说明。

**[P3] export --masked 的遮蔽只发生在客户端，网络与审计层面等同明文导出**（admincli.go:97、106-127）

`--masked` 仍调用同一个 `/api/v1/export`（服务端返回明文，见 internal/api/api.go:117-125，测试 mock 亦返回 "real"），CLI 把 `is_secret` 字段替换为 `***` 后输出。BaseURL 为 http 且跨机使用时，全部敏感值已经明文过网；服务端审计也记为普通（明文）导出。功能语义（"用于分享结构"）成立，但建议在 `--masked` 的提示里明确"服务端按明文导出返回并记审计"，避免使用者产生"遮蔽导出更安全"的误解。

**[P3] usage 帮助文本缩进错位（tab 混入）**（cli.go:538-539）

usage 字符串中 `keyhive login`、`keyhive status` 两行行首是 tab，其余行是空格，终端渲染时这两行会整体右移错位。顺带一提：`keyhive help` 经 `cli.Run` 的 default 分支返回 2（与参数错误同码，cmd/keyhive/main.go:45 将 help 路由进 Run），惯例上 help 应返回 0——属 cmd 层与 cli 层的联合行为，一并记录。

## 做得好的地方

1. **stdout/stderr 纪律严格**：所有人类可读的警告、进度、审计提示（`⚠️`/`✅`）一律走 stderr，stdout 只输出服务端 JSON；totp 尤其干净——stdout 仅 6 位码 + 换行，条目上下文在 stderr，可直接 `$(keyhive totp ...)` 嵌入命令。
2. **凭据不外泄**：status 只打印"已配置/未配置"；tokenHint 不回显令牌；服务端错误 body 透传不含凭据；admin 密码默认走 `KEYHIVE_ADMIN_PASS`，`--pass` 的 flag 帮助文本明确警告 shell 历史风险；login 交互输入令牌用 `term.ReadPassword` 遮蔽回显、管道下自动降级。
3. **edit 的遮蔽回传闭环严谨**：遮蔽 GET → 客户端仅改提及字段 → PUT 回传 `***` → 服务端 `UpdateEntry`（internal/store/store.go:147-154）保留原值；CLI 还显式拦截"把敏感字段设为字面 `***`"（admincli.go:353-356），防止用户误把遮蔽占位写成真实密码。import 侧对遮蔽版导出 JSON 在解析层整体拒绝（internal/importer/keyhive.go:32-34）。
4. **login 合并写入设计周到**：仅覆盖本次提供的项、写前用 token_read 实测连通性（拒绝写坏配置）、坏 JSON 拒绝覆盖并提示、非交互模式报错而非挂死、`--no-verify` 显式逃生口。
5. **URL/编码处理正确且有回归测试**：中文搜索词 `url.QueryEscape`（附 GBK 坑回归测试）、id 用 `url.PathEscape`、CSV 走标准库 `encoding/csv`（LazyQuotes、剥 BOM、容忍列数不齐）。

## 测试盲区

- `promptSecret` 的 `term.ReadPassword` 成功路径需要真终端，CI 无法覆盖；遮蔽回显全靠手工验证。
- import 的"每条登录"行为不可见：mock 服务端不统计登录次数，没有任何断言能暴露 N 条导入触发 N 次 `/auth/login`（P1 问题的测试盲区）。
- stdout 纯净性无反向断言：reveal/totp/add/export 的测试只 `Contains` 检查预期内容，未断言 stdout **不含** stderr 的警告行（如 "⚠️"），管道兼容性靠纪律维持。
- 管道端到端场景缺失：`reveal | docker login --password-stdin` 式用法无测试（对应 P3 的 JSON 包装问题）。
- 超时路径未测：10s/15s 超时、服务端慢响应、请求中途断连（`io.ReadAll` 错误也被忽略）。
- `adminFlags` 的 Parse 失败路径（打错 flag 仍继续执行）无测试。
- config 权限与并发：已有 0644 文件更新后的权限保持（Linux）、并发 login、写入中断导致的半截文件，均无覆盖（Windows 上跑测试也无法暴露权限问题）。
- CSV 边界的端到端：字段含逗号/引号/内嵌换行/CRLF、GBK 或 UTF-16 编码的 Excel 导出（当前仅支持 UTF-8，非 UTF-8 会乱码入库而不报错）、数千行大文件。
- `cmdStatus` 在 HTTP 200 但响应非数组时静默无结论直接返回 0（cli.go:466-471），未覆盖。
- `filterStale`/`countStale` 依赖服务端 `updated_at` 恒为标准 RFC3339；格式变化时静默不过滤，无测试锁定。
