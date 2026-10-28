# 角色复审 R1 — SRE + 测试质量（2026-10-28）

对象：工作区未提交改动（02-test-report.md 第 3 节 19-21 项交付修复 + main.go 超时）。
方法：逐文件比对 + 本机 bash 模拟 workflow 逻辑 + 独立复跑全量测试/覆盖率，不信任原报告数字。

## 交付核验（逐项）

### 1. .dockerignore × Dockerfile 多阶段 COPY 链 — ✅（附 2 条防御性建议）

全仓仅一处 `go:embed`（`web/web.go:7`，`all:dist`）。构建链逐条比对：

| Dockerfile 步骤 | 需要的上下文 | .dockerignore 影响 | 结论 |
|---|---|---|---|
| web 阶段 `COPY web/package*.json` | package.json + package-lock.json | 均未排除 | ✅ `npm ci` 可用 |
| web 阶段 `COPY web/ ./` + `vite build` | index.html / public/ / src/ / tsconfig / vite.config.ts | 仅排除 node_modules、dist、coverage、`*.md`（web/ 下无 md） | ✅ 齐全 |
| build 阶段 `COPY go.mod go.sum` | 根部两文件 | 未排除 | ✅ |
| build 阶段 `COPY . .`（第 22 行） | cmd/ internal/ web/web.go | 排除 .git/.github/docs/skills/md/jar/exe 等均非编译输入 | ✅ |
| `COPY --from=web /src/web/dist ./web/dist`（第 23 行） | — | 本地 web/dist 被排除**无影响**：镜像内 dist 在 `go build`（第 24 行）之前注入，embed 可见 | ✅ 关键顺序正确 |

- **data/ 排除是真实防护而非形式**：本机 `data/` 实测含 `master.key`、`keyhive.db(-wal/-shm)`，不排除即随上下文进 daemon。
- ⚠️ P3：未排除 `.env*`。当前仓库无 .env，但 `COPY . .` 层会固化未来误放的敏感文件，建议顺手补一行。
- ⚠️ P3：未排除根级发版产物 `keyhive-*.tar.gz/.zip`（同理，防御性）。

### 2. Dockerfile /app/data 预建属主 — ✅（bind mount 场景有一处未覆盖，见建议）

`mkdir -p /app/data && chown 1000:1000 /app/data`（第 29-30 行）与 `adduser -u 1000` 对齐。Docker 匿名/命名卷首次挂载时的 copy-up 会保留镜像内目录属主 → 修复了非 root 首启 EACCES，注释描述准确。`USER keyhive` + `VOLUME` + `EXPOSE 8020` + `KEYHIVE_ADDR=:8020` 自洽。

- ⚠️ P2：**bind mount 不走 copy-up**。README/compose 主用法 `-v ./data:/app/data` 在 Linux 宿主上若目录不存在，由 dockerd 以 root 创建 → uid 1000 仍会 EACCES，镜像内 chown 管不到。建议 README 注明"宿主目录需可被 uid 1000 写入（或 `chown -R 1000`）"。
- P3：无 HEALTHCHECK（当前无 /health 端点，属重构项，不阻塞）。

### 3. docker-image.yml VERSION 解析 — ✅

本机 bash 按 workflow 原样模拟（不空谈，实测）：

| image_tags 输入 | TAGS | 解析出的 VERSION |
|---|---|---|
| `1.4.0,latest` | 1.4.0 latest | **1.4.0**（首个非 latest）✅ |
| `latest` | latest | **dev**（回退，与 compose `${KEYHIVE_VERSION:-dev}` 对齐）✅ |
| `v1.4.0`（顺手测） | v1.4.0 | 1.4.0（剥 v 前缀）✅ |

amd64/arm64 两 job 逻辑逐行一致；merge-manifest 不需要 VERSION（只拼 manifest）正确。

- ⚠️ P3：尾随逗号 `1.4.0,` 会产生空 tag 元素（`-t repo:-amd64`）在 build 阶段才报错，可加输入校验提前拦截。
- P3：与 release-binary 的版本口径不一致——镜像注入 `1.4.0`（无 v），二进制注入 `v1.4.0`（带 v，release-binary.yml:122），`keyhive version` 输出将差一个 v。建议统一。

### 4. release-binary.yml 权限模型 — ✅

- workflow 级 `permissions: contents: write` 已删除；三个 job 全部显式声明：build-frontend `read`、build-binary `read`、release `write`。job 级显式声明会覆盖组织/仓库默认权限 → 无论仓库默认是 read 还是 write 都最小化。✅
- build job 确实只需 read：checkout + setup-node/go + upload/download-artifact（artifact 走 actions 自有授权，不吃 GITHUB_TOKEN contents）+ 本地编译，无任何 GitHub 写操作。✅ 唯一 `contents: write` 给真正建 Release 的 softprops 步骤，模型正确。
- tag 触发时 `github.event.inputs.*` 为空：REF_TYPE=tag 分支优先取 tag 名，INPUT_VERSION 回退值不会被误用；USE_CN_MIRROR 空串 ≠ "true" 不会误切国内源。✅
- docker-image.yml：workflow 级 `contents: read`，推镜像用外部仓库凭据 `--password-stdin`，密码不落日志（打印用户名可接受）。✅

### 5. USE_CN_MIRROR / 内网地址规范 — ✅

grep 交付链路全部文件（Dockerfile / compose / .dockerignore / 两 workflow / vite.config）：**零内网地址**（无 192.168.x、harbor.sktill.top、mirrors.lpx.com、gh 代理）。仅公网镜像源 npmmirror / goproxy.cn，参数名 USE_CN_MIRROR 符合全局规范（ZONE 或 USE_CN_MIRROR 二选一）。compose 本机默认 `"1"`（内网自建场景合理）、CI 默认 false 且注释注明 runner 在国外，取向正确。

## 超时配置核验

- **ReadHeaderTimeout=10s ✅**：slowloris 慢头部连接 10s 断开，对正常移动客户端余量充足。
  - ⚠️ P2 残余：未设 ReadTimeout → **慢速 body**（每秒滴 1 字节）无时间上限，仅受 10MB `MaxBytesReader` 量上限（api.go:24-36，ContentLength 预判 + MaxBytesReader 映射 413）→ 恶意客户端可长期占用连接 + goroutine。建议进重构：非 SSE 路由加 per-route ReadTimeout（或部署文档强制前置反代）。
- **IdleTimeout=120s × SSE 交互 ✅**：Go 语义确认——IdleTimeout 只管 keep-alive **两请求之间**的等待。SSE 连接处于活跃响应状态，不属于 idle，长流不会被切断；闲置 keep-alive 连接 120s 回收。注释判断正确。
- **无 WriteTimeout 取舍 ✅（注释属实）**：WriteTimeout 覆盖整个响应写出期，会给 SSE 流压顶。SSE 服务的标准做法就是不设，靠客户端断开传播兜底——已确认 `ChatStream` 持有 `r.Context()`（aichat_api.go:285）且上游请求 `NewRequestWithContext`（stream.go:107）→ 浏览器断开会级联取消上游请求，不会白流。
  - ⚠️ 观察：上游 `http.Client{Timeout: 120s}`（stream.go:117；anthropic.go 流式 120s）**覆盖流式 body 读取**，即 AI 流实际硬上限 120s。与"服务端不设 WriteTimeout 保长流"组合无冲突（当前聊天够用），但若未来要求更长的生成/Agent 流，需把上游改为"ResponseHeaderTimeout + 无总超时 + 心跳看门狗"，届时此处要一并改。
- **runServe 改造 ✅（本轮最关键的一处）**：原 `http.ListenAndServe(addr, srv.Handler)` 会另建零配置 server，buildServer 里设置的超时**全部静默失效**；现 `srv.Addr = addr; srv.ListenAndServe()` 使超时真正生效。改法正确。
- P3：无信号量优雅停机（SIGTERM 直接终止，`defer db.Close()` 不执行）。SQLite WAL 正常无损，仅极端情况掉一次 checkpoint，重构项。

## 测试报告抽查与独立跑测

| 报告声明 | 独立复现结果 | 结论 |
|---|---|---|
| `go test ./... -count=1` 17 包全 ok，退出码 0 | 17 包 ok + web `[no test files]`，exit 0 | ✅ 一致 |
| vitest 141/141，退出码 0 | 141/141（13 文件），exit 0 | ✅ 一致（一条 ElOnlyChild stderr 噪音，不影响判定） |
| 覆盖率 totp 100 / aiapi 100 / api 90.0 / store 86.6 | 抽测四包：100.0 / 100.0 / 90.0% / 86.6% | ✅ 逐位吻合 |
| §2 新增测试文件（15 行清单） | 与 git untracked 测试文件逐一对应，无缺失 | ✅ |
| "新增 60+ 用例" | 新文件 `func Test*` 实测 84 个（不含子用例） | ✅ 保守陈述，真实 |
| `go build` / `go vet` 通过 | 均退出码 0 | ✅ |
| §3 "恰好 **17** 条红测、9 包 FAIL" | TestBug_ 函数现存 **16** 个；报告自己的日志块也只列 16 行；"9 包 FAIL" 正确（aiapi/aichat/api/auth/cli/crypto/importer/mcpserver/store） | ❌ 数字夸大 1，口径错误但不影响修复真实性，建议改为 16 |

结论：报告整体**真实可复现**，覆盖率和用例数可信；唯一瑕疵是红测计数 17→16 的笔误级偏差。

## CI 缺口（G1 紧迫性评估）

现状：`.github/workflows/` 只有 docker-image.yml（手动）与 release-binary.yml（手动/tag）——**push/PR 零 CI**，全部测试资产只在本机 Windows 跑。风险具体化：

1. 本机无 cgo 跑不了 `-race`（报告 §5 自认）——而本轮刚修过 store 轮换一致性、前端搜索竞态这类并发/时序缺陷，Linux `-race` 防线缺位正打在软肋上。
2. **发版流水线不跑测试**：release-binary.yml 红测状态也能产出带 sha256 的正式 Release；docker-image.yml 同理可推镜像。测试门禁与交付动作完全脱钩。
3. vitest ≥90% thresholds 只在本机把关，无 PR 门禁；`go-version-file` 钉版本的 Go 版本漂移也无人验证。
4. 本轮测试投入已滚到 84 个新 Go 函数 + 141 前端用例，防线却单点依赖一台开发机（环境损坏/人为跳过即全失守）。

**评估：P1，紧迫性高，成本极低**（一个 ubuntu job：setup-go + `go vet` + `go test -race ./...`；一个 node job：`npm ci` + `vitest run --coverage`；照抄 release-binary 的缓存与镜像切换写法即可）。与 03-refactor-plans 的 v1.4.0 首位排期一致，建议本轮改动一并提交 ci.yml，且让两个交付 workflow `needs` 测试 job（或 branch protection required check 兜底）。

## 建议（分级）

- **P1（立即，随本轮提交）**：新增 `.github/workflows/ci.yml`：push/PR 触发 `go vet` + `go test -race ./...`（Linux）+ `npm ci && vitest run --coverage`；docker-image/release-binary 对测试设门槛（needs 或 required check）。
- **P2**：
  - README 补 bind mount 属主说明（`./data` 需 uid 1000 可写，或 `chown -R 1000`）——预建属主修复覆盖不到的主部署路径。
  - 非 SSE 路由增加 body 读取时间上限（per-route ReadTimeout / TimeoutHandler 变体），补上慢 body 残余面。
- **P3**：
  - .dockerignore 补 `.env*` 与根级 `keyhive-*.tar.gz`/`*.zip`。
  - docker-image.yml image_tags 加尾随逗号/空元素校验；统一镜像与二进制的 v 前缀口径。
  - 信号量优雅停机（SIGTERM → srv.Shutdown + db.Close）。
  - Dockerfile HEALTHCHECK（待 /health 端点）。

## 总体结论

**通过（PASS，含观察项）。** 第 19-21 项交付修复与 main.go 超时改造全部核验属实：.dockerignore 不会误伤 go:embed 构建链、VERSION 解析两种输入实测正确、权限模型最小化到位、交付链路零内网地址；超时三元组（10s/120s/无 WriteTimeout）与 SSE 的交互判断正确，且 `srv.ListenAndServe` 修复使超时真正生效（此前形同虚设）。测试报告独立复现全绿、覆盖率逐位吻合，仅红测计数 17 应为 16 的口径偏差。唯一实质缺口是 G1（CI 无测试门禁、发版与测试脱钩），建议按 P1 随本轮一并补上。
