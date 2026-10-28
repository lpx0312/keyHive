# 重构方案（基于 2026-10-28 全仓审查 107 项发现）

> 本轮已修复：P0×1 + P1×8 + 高价值 P2×20（见 [02-test-report.md](02-test-report.md) 第 3 节）。
> 本文档是**其余 P2/P3 的分批重构路线**：按专项组织，每项含现状/方案/验收/优先级（P1 高 P2 中 P3 低）与工作量（S≤半天 M≤2天 L>2天）。
> 原则：小步提交、每步有测试锚定、不破坏对外契约（API/CLI/MCP/镜像）。

## 专项 A：安全加固（建议下一版优先）

### A1. 轮换进程级写互斥 〔P1·M〕
- 现状：~~rotate 与条目并发写仍有 TOCTOU 窗口~~ **R2 已落地最小版**：`store.writeMu` 覆盖 Create/Update/Delete/RotateAllEntries（R1 复审确认并提前实施，并发测试锚定）。
- 剩余：读方法与批量删除/复制的锁覆盖核对（R2 审计确认已收敛到已锁方法）、**putSetting 密文写（LLM Key 保存）未过 writeMu（R2 审计指出的残余窗口）**、锁粒度优化（RWMutex 读锁）、轮换期间对客户端返回"维护中"的体验。方案与验收同下。
- 方案（剩余部分）：`store.Store` 加 `sync.RWMutex`——所有写方法取写锁，读方法取读锁。单实例部署下彻底关闭窗口。
- 验收：新增并发测试（goroutine 并发 Create + Rotate，断言所有条目轮换后可解密）；现有轮换测试全绿。
- 关联：store-templates P1-2 / http-api P1。

### A2. 主密钥派生与 key_check 强化 〔P2·M〕
- 现状：`KEYHIVE_MASTER_KEY` 环境变量路径仅单次 SHA-256 派生，库泄露后可离线高速爆破；`Decrypt` 对非法密文存在明文透传路径，使 key_check 校验可被构造绕过（crypto-totp P2-2/P2-3）。
- 方案：①env 路径改 argon2id（time=1, memory=64MB 级）派生（文件路径保持直读，无需 KDF）；②`Decrypt` 收紧：非法格式一律报错不透传，key_check 用专用 verify 函数（解密已知明文常量比对，不落库明文）；③为将来引入 `enc:v2:`（GCM AAD 绑定 entry_id/field，防密文移植）预留版本位，读侧按前缀分派。
- 验收：现有 crypto 全测试绿 + 新增"env 弱口令派生耗时断言""非法密文不透传"测试；**迁移**：老库不动（仍 enc:v1），轮换时逐条升级 v2。
- 关联：crypto-totp P2/P3。

### A3. 会话治理 〔P2·S〕
- 现状：会话滑动续期无绝对寿命上限（被盗 cookie 永不过期）；过期会话/令牌无清理；cookie 无 Secure 开关；登录时序可枚举用户名（auth P3、api P2）。
- 方案：①会话加绝对寿命（默认 7d，`KEYHIVE_SESSION_MAX_AGE` 可调），续期时检查；②启动时 + 每小时清理过期 sessions/tokens；③`KEYHIVE_COOKIE_SECURE=1` 开关（默认关——当前 HTTP 内网部署为主，文档注明 HTTPS 反代时开启）；④登录路径用户不存在时执行等耗时 dummy argon2 verify。
- 验收：绝对寿命/清理/时序均衡各有测试；默认行为不变（现有测试绿）。
- 关联：auth P3-2/3/4、api P2。

### A4. AI 出站治理（SSRF 边界） 〔P3·S〕
- 现状：ai-config 的 base_url 可指向任意地址（本轮已收紧到 admin-only，但 admin 误配/被钓仍是面）；test/models 端点错误回显响应体 200 字符。
- 方案：base_url 校验必须 http(s) 且非 userinfo 形态；错误回显截断到 120 字符并过滤控制字符；提供 `KEYHIVE_AI_EGRESS_ALLOWLIST`（可选，逗号分隔域名后缀，默认空=不限——内网自托管访问内网 LLM 是合法场景，不做硬限制）。
- 验收：校验与截断测试；README 配置表更新。
- 关联：ai-integration P2/P3、http-api P0 余波。

## 专项 B：数据与校验

### B1. model 长度上限体系 〔P2·S〕
- 现状：`Validate` 无任何长度/数量上限（本轮 API 层已有 10MB MaxBytesReader 兜底，但单字段 900KB 明文仍可入库，放大 LLM 上下文与列表体积）。
- 方案：title≤512、字段 key≤128、description≤4096、单字段 value≤1MB（明文口径）、字段数≤64、tags 单个≤32 且≤16 个；`Validate` 统一返回带字段名的错误；前端 EntryEdit 同步 maxlength 提示。
- 验收：表驱动边界测试；老数据不受影响（只拦写入）。
- 关联：data-foundation P2-2。

### B2. db seed 原子化 〔P3·S〕
- 现状：seed 的 admin INSERT 与 'seeded' 标记两次独立 autocommit，首启窗口崩溃后 Open 撞 UNIQUE 永久失败（data-foundation P2-1）。
- 方案：seed 整体包进单事务（BEGIN IMMEDIATE）；或改 `INSERT OR IGNORE` + 幂等标记。
- 验收：模拟中断（事务内强制失败）后二次 Open 成功的测试。
- 关联：data-foundation P2-1、P3 连接池注释陷阱（提供 `WithTx` helper 固化"事务内禁止再取连接"约束并注释）。

## 专项 C：API 层工程化

### C1. 错误响应收敛 〔P3·M〕
- 现状：`writeErr/writeInternalErr/writeReadErr` 三套并存，部分 400 文案与 5xx 边界靠人工维持（http-api P3）。
- 方案：收敛为 `respondError(w, code, kind, msg)` 一个出口；内部错误一律 log+通用文案（纪律已建立，本次是结构化收敛）；可选附 `error_code` 字段供前端程序化处理。
- 验收：现有 40+ api 测试全绿（文案断言的个别调整）。

### C2. 请求参数绑定 helper 〔P3·S〕
- 现状：id 解析/分页参数在每个 handler 手写（本轮已补齐边界测试锚定行为）。
- 方案：`parseID`/`parsePage` 两个 helper 统一，删除散落解析。
- 验收：纯重构，测试不变全绿。

## 专项 D：AI 集成

### D1. 双协议 provider 抽象 〔P3·M〕
- 现状：OpenAI 与 Anthropic 两套实现平行展开（Chat/Stream/Models/TestConn 四处 ×2），新增协议要改多点；端点默认分派刚修过，抽象后这类问题归零（ai-integration P3）。
- 方案：`provider` 接口（Chat/ChatStream/ListModels/TestConnection），`registry[protocol]` 注册；预设表只管元数据。
- 验收：现有双协议测试作为契约回归，行为不变。

### D2. 遮蔽契约内聚 〔P3·S〕
- 现状：遮蔽/隐身仅靠调用方注释约定，aichat 包无兜底（ai-integration P3-5）。
- 方案：提供 `aichat.MaskSecrets(entries)` 兜底 helper，SSE 出站前强制过一道；与 aiapi 的遮蔽实现共享同一函数（挪到 model 或独立 mask 包，避免 import 环）。
- 验收：新增"漏遮蔽防护"测试（构造敏感条目直发，断言出站已遮蔽）。

## 专项 E：CLI / MCP 体验

- E1. `reveal --value-only` 纯值输出（cli P3-5，`| docker login --password-stdin` 直连）〔P3·S〕
- E2. kh_status 报令牌配置状态（mcp P3-2，消除"401 令牌无效"误导）〔P3·S〕
- E3. admin 通道超时按命令分级：rotate/export/import 用长超时（如 10m），其余 15s（cli P2-4）〔P2·S〕
- E4. CSV 导入错误信息带行号（LazyQuotes 收紧后补体验）〔P3·S〕
- E5. MCP 工具结果对超长值截断保护（防把库刷进 AI 上下文）〔P3·S〕

## 专项 F：前端架构

### F1. 路由守卫与请求层统一 〔P2·M〕
- 现状：无路由守卫（未认证闪现后 401 跳转）；fetch 无超时/取消；搜索防抖缺失（本轮已修请求序号竞态）（frontend P2/P3）。
- 方案：①路由 meta `{requiresAuth, requiresAdmin}` + 全局 beforeEach（未登录直接 /login，非 admin 进 /users 等直接拦）；②api.ts 统一封装 `withTimeout(signal, 15s)` 与可选 AbortController 透传；③搜索 composable `useDebouncedSource()`（250ms）。④**Audit.vue:75-99 同类竞态 + 无 catch（R1 前端复审补录，01-review P3-6），随本专项一并修**。
- 验收：守卫单测（直输 URL 不闪现）；超时路径测试；既有 141 前端测试全绿。
- 关联：frontend P2-1/P2-4/P2-5。

### F2. ChatDrawer 覆盖率口径 〔P3·S〕
- 现状：vitest coverage.exclude 仍排除 ChatDrawer.vue，测试已补但统计不计入。
- 方案：从 exclude 移除，补齐缺口到 thresholds 过。（R1 复盘：纳入统计后 ChatDrawer Branch 58.86%、全局 Lines 跌至 85.33% 不过线，工作量 S→M）

## 专项 G：测试与 CI

- G1. **CI 加 go test + vitest job**（push/PR 触发，Linux 跑 `-race`）——**已随本轮落地**：`.github/workflows/ci-test.yml`（vet + `go test ./... -race` + vitest，最小权限）。原风险记录：此前测试只在本机跑、发版流水线不跑测试即可交付，防线与交付脱钩。若不希望 push 触发 CI，删除该文件或加 paths 过滤即可。
- G2. GitHub Actions pin 到 commit SHA、`permissions` 最小化全面复查（bootstrap P3）〔P3·S〕
- G3. store 新增修复路径（轮换 settings、坏行隔离分支）补覆盖锚点，回到 89%+ 〔P3·S〕
- G4. 登录防爆破：暴露公网前必做（README 路线图既有决策，方案：失败 5 次指数退避 + 常量时间响应）〔P2·M，触发条件=暴露公网〕

## 专项 H：文档与发版

- H1. CHANGELOG 常态化（本轮已建 CHANGELOG.md，后续每个版本一节）〔P3·S〕
- H2. README 与模块文档联动检查进发版 checklist（bootstrap P3：裸机 fresh clone 白屏提示——dist 未跟踪时的 `npm run build` 前置说明已存在，checklist 强调）〔P3·S〕
- H3. skills/keyhive/SKILL.md 与 CLI 子命令同步审校（本轮无漂移，纳入 checklist）〔P3·S〕

## 各模块速赢清单（P3 杂项，按模块归档）

| 模块 | 项 |
|---|---|
| crypto | master.key 存在时不复查 0600（启动时 chmod 一次）；密钥材料 zeroize（Go 局限，注释说明） |
| auth | Bearer 解析容忍多空格（行为已 fail-closed，仅体验）；改密后踢掉其他会话已有——补审计 detail |
| db | `nowUTC`/`marshalFields` 已测；TestOpenBadPath 去除 Windows 耦合 |
| api | /auth/login 与 /api/v1/ai 的 404 语义统一（unknown path 统一 JSON 404） |
| cli | help 文本与 README 命令表逐条同步 |
| web | Element Plus 按需引入（构建体积）；Settings emoji 图标 alt 文案 |
| importer | Chrome CSV 的 note 字段同名处理与 Bitwarden 对齐复核 |

## 实施节奏建议

1. **下一版（v1.4.0）**：G1（CI 测试防线）→ A1（轮换互斥）→ A3（会话治理）→ B1（长度上限）——安全与防线优先。
2. **v1.4.x 维护版**：A2（KDF 强化，含 enc:v2 迁移设计）、C1/C2、E1-E3。
3. **v1.5**：D1 provider 抽象、F1 前端架构、G2。
4. 触发式：G4（暴露公网时）、A4（接外网 LLM 服务时）。
