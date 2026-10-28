# 入口与交付链路（bootstrap）代码审查报告

> 审查日期：2026-10-28 · 审查人：代码审查 agent · 只读审查
> 范围：`cmd/keyhive/`（main.go、main_test.go、serve_test.go）、`internal/version/`、`Dockerfile`、`docker-compose.yml`、`.github/workflows/release-binary.yml`、`.github/workflows/docker-image.yml`、`.claude-plugin/plugin.json`、`.claude-plugin/marketplace.json`、`skills/keyhive/SKILL.md`、`README.md`、`go.mod`、`.gitignore`、`.zcodeignore`

## 概览

共审查 14 个文件（全部通读，未抽样）。整体质量高：main.go 结构清晰、buildServer 可测化设计扎实，e2e 测试覆盖登录→CRUD→reveal→TOTP→令牌→轮换→审计全链路；两条 CI 流水线的凭据处理干净（--password-stdin、secrets 只经 env 注入、无 echo），docker-image.yml 权限已最小化且无任何内网地址，符合项目规范。Dockerfile 是规范的多阶段构建、非 root 运行、最终镜像只有一个二进制，密钥不可能进镜像层。未发现 P0/P1 问题。主要缺口集中在交付链路的"最后一公里"：构建上下文无 .dockerignore（本地密钥库会进 build context）、/app/data 属主问题、CI 构建的镜像版本恒为 dev、http.Server 无超时，以及 release workflow 权限粒度过粗。

## 发现清单

### P0（阻断）

无。

### P1（严重）

无。

### P2（一般）

**[P2] 构建上下文无 .dockerignore，本地密钥库与杂物进 build context**（Dockerfile:22，仓库根确认无 `.dockerignore`）
问题：`COPY . .` 会把工作副本中的 `data/`（含 `master.key` 与 `keyhive.db`）、`keyhive.exe`（23MB）、`admin.jar`、`web/node_modules/`、`.git/` 全部送入 docker daemon 与 build cache。多阶段构建保证这些文件不进最终镜像（运行层只 COPY `/out/keyhive`），但：密钥库进入了本机 daemon 的 build cache 层；若将来改用远程 builder（如 buildx remote），等于把主密钥发出去；23MB 二进制 + node_modules 也严重拖慢每次构建。
修复：新增 `.dockerignore`，至少排除 `data/`、`tmpdata/`、`*.exe`、`*.jar`、`.git/`、`web/node_modules/`、`web/dist/`（dist 由 web 阶段产出后 COPY）。

**[P2] /app/data 未预创建并 chown，非 root 用户在常见部署路径下无法写入**（Dockerfile:28-33）
问题：`adduser -D -H` 后镜像内不存在属主为 keyhive 的可写目录，`VOLUME ["/app/data"]` 指向的目录在镜像中不存在（由 Docker 运行时以 root 创建）。后果分两种：命名卷/匿名卷场景目录 root 属主 → uid 1000 写 `master.key`/`keyhive.db` 直接 EACCES；README 与 docker-compose.yml 主推的 bind mount（`./data:/app/data`）在 Linux 上 Docker 自动创建的宿主目录同样 root 属主 → 首启 `log.Fatal` 崩溃循环。README 明确面向群晖/NAS 场景，该路径真实可触发。
修复：在 `VOLUME` 前加 `RUN mkdir -p /app/data && chown keyhive:keyhive /app/data`（修复命名卷路径）；bind mount 场景在 README/compose 注释中说明需 `chown 1000` 或提供 entrypoint 降权方案。

**[P2] http.Server 完全无超时配置，且 buildServer 组装的 Server 对象被弃用**（cmd/keyhive/main.go:68, 142）
问题：`buildServer` 返回的 `&http.Server{Handler: r}` 只设置了 Handler，`runServe` 实际调用 `http.ListenAndServe(addr, srv.Handler)`——等于用默认零值配置另起服务，ReadHeaderTimeout/ReadTimeout/IdleTimeout 全部为 0。对密钥管理服务这意味着 slowloris 慢连接可无限占用连接句柄，单连接也可长期挂起（gosec G112 会标）。内网部署降低了暴露面，但这是安全基线项。
修复：直接 `srv.Addr = addr` 后 `srv.ListenAndServe()`，并至少设置 `ReadHeaderTimeout`（如 10s）与 `IdleTimeout`（如 120s）。注意项目含 SSE 端点（main.go:188 注释自证），**不要**设全局 WriteTimeout，否则会切断长连接。

**[P2] CI 构建的镜像版本恒为 dev，版本注入链路断线**（.github/workflows/docker-image.yml:98, 154）
问题：Dockerfile 定义了 `ARG VERSION`（Dockerfile:16），docker-compose.yml:10 也正确传递 `VERSION: ${KEYHIVE_VERSION:-dev}`，但 docker-image.yml 的 `docker build` 只传了 `CN_ARGS`（USE_CN_MIRROR），从不传 `--build-arg VERSION=...`。结果：推到 ACR 供群晖用户 `docker pull` 的官方镜像，`keyhive version` 与 Web UI 设置页永远显示 `dev`，无法判断运行版本，回滚固定版本号的 README 建议也无从对应。
修复：给 workflow 增加 `image_version` 输入（默认取 tag/手填），拼进 `--build-arg VERSION=${IMAGE_VERSION}`。

**[P2] release workflow 的 contents:write 权限作用于全部三个 job**（.github/workflows/release-binary.yml:12-13）
问题：`permissions: contents: write` 声明在 workflow 级，对 `build-frontend`、`build-binary` 两个纯构建 job 同样生效，违反最小权限原则；构建 job 一旦被依赖链中的恶意代码（如 npm install 的投毒包，该 job 恰好在 install 后执行任意构建脚本）劫持，令牌即具备改写仓库/release 的能力。对比 docker-image.yml:3-4 做法正确（workflow 级 contents: read）。
修复：把 `contents: write` 下沉到 `release` job 级，其余 job 保持默认 read。

### P3（建议）

**[P3] 版本号格式三通道不一致，Commit 变量从未被注入**（.github/workflows/release-binary.yml:120；internal/version/version.go:9）
问题：release 二进制注入的是带 `v` 前缀的 tag（`outputs.tag` = `v1.2.3`），docker-compose 本地构建约定不带 v（`KEYHIVE_VERSION=1.2.3`），plugin.json 又是 `1.2.4`——三个通道显示格式互不一致，用户比对"CLI 版本 vs 服务端版本"时易困惑。`version.Commit` 字段有拼接逻辑但全部构建链路（workflow、Dockerfile）都只注入 Version，Commit 恒为 "none"，属死代码。
修复：统一去掉 v 前缀（ldflags 用 `outputs.version`）；workflow 与 Dockerfile 补 `-X ...Commit=${{ github.sha }}`，或删掉 Commit 字段。

**[P3] workflow 输出值直接内插进 run:，与自身已有的 env 隔离写法不一致**（.github/workflows/release-binary.yml:120, 123-125, 182-183, 188-189）
问题：`INPUT_VERSION` 通过 env 注入 shell（正确姿势，line 104-105），但产出的 `steps.ver.outputs.version/tag` 又用 `${{ }}` 直接内插进 run 脚本。版本号输入虽要求 workflow_dispatch 权限（非不可信输入，无 pull_request_target 场景），但含引号/反引号的恶意版本号仍可在构建与 release 步骤执行任意命令，属纵深防御缺口；Action 也全部按 major tag（@v4/@v2）而非 commit SHA pin。
修复：outputs 一律经 env 传入 shell；关键 Action（checkout、gh-release）改 SHA pin。

**[P3] dispatch default 分支 log.Fatalf 后 return 2 不可达，实际退出码为 1**（cmd/keyhive/main.go:53-54）
问题：`log.Fatalf` 直接 os.Exit(1)，其后 `return 2` 永不执行，注释承诺的退出码 2 与实际不符；且 Fatalf 使该分支不可测（serve_test.go:69 已注明只能跳过），错误信息还带 log 时间戳前缀，CLI 观感差。
修复：改为 `fmt.Fprintf(os.Stderr, ...)` + `return 2`，测试即可覆盖未知子命令分支。

**[P3] README 裸机章节"已预构建 web/dist 可跳过"与仓库实际不符**（README.md:68；.gitignore:11；web/web.go:5）
问题：`.gitignore` 忽略 `web/dist/assets/`，git 只跟踪 `web/dist/index.html` 与 `favicon.svg`（已用 `git ls-files` 证实），web.go 注释也自述"仓库内含占位 index.html"。fresh clone 后直接 `go build`（README 给的路径）能编译成功（embed 到什么算什么），但 index.html 引用的 `/assets/index-D5vjkQix.js` 不存在——得到一个**静默白屏**的二进制，无任何报错。
修复：README 改为"首次构建必须先 npm ci && npm run build"，或在构建时校验 dist/assets 存在（go:generate 或预检脚本）给出明确错误。

**[P3] Dockerfile/compose 均无 HEALTHCHECK**（Dockerfile:26-35；docker-compose.yml）
问题：二进制内置未认证的 `/api/v1/version`（internal/api/webui.go:19-21）是现成探针，但镜像与 compose 都未定义健康检查，`restart: unless-stopped` 只能发现进程退出，无法发现假死（如 SQLite 锁死导致的请求挂起）。
修复：Dockerfile 加 `HEALTHCHECK CMD wget -qO- http://127.0.0.1:8020/api/v1/version || exit 1`（alpine 需补 busybox wget，已内置），或 compose 加 healthcheck 映射。

**[P3] 发版流水线不执行任何测试即出产物**（.github/workflows/release-binary.yml）
问题：两条 workflow 从触发到发布 Release/推镜像全程无 `go test ./...` 与前端 vitest。本地 90.4% 覆盖率的价值在 CI 缺位时只保护提交者本人的机器，手滑在未跑测试的提交上打 tag 即直接发版。
修复：release-binary.yml 增加 test job（`needs: test`），或加独立的 tag 触发 CI workflow。

## 做得好的地方

1. **CI 凭据卫生**：docker-image.yml 全程 `--password-stdin`、secrets 只通过 env 块注入 shell（无 `${{ }}` 直接内插敏感值）、无 `set -x`、用户名取自 vars 而非 secrets；workflow 级权限 `contents: read` 最小化，是两个 workflow 中权限控制的正确范本。
2. **Docker 镜像安全形态**：多阶段构建、`adduser -D -H -u 1000` 非 root 且无 home、运行层仅 COPY 一个静态二进制（CGO_ENABLED=0）——密钥材料只存在于运行时数据卷，结构上不可能泄入镜像层；ca-certificates/tzdata 齐全。
3. **可测性设计**：buildServer 让 runServe 与 e2e 共用同一组装路径，测试覆盖真实文件库 + embed 前端 + Bearer AI 令牌 + 轮换后可解密 + 审计动作齐全性，错误路径（密钥篡改、非法目录）也有专测（main_test.go:340-359）。
4. **spaHandler 缓存策略及其注释**：index no-cache + assets hash 文件名 immutable 的组合正确，注释把 embed 无 modtime、FileServer 不发 Last-Modified 的坑写清楚了，并配了直接单测（含"不存在的 assets 回退 no-cache"边界）。
5. **规范合规**：全范围 grep 无任何内网地址（192.168.0.x / mirrors.lpx.com / harbor.sktill.top / 本机代理）；国内源一律走 `use_cn_mirror` / `USE_CN_MIRROR` 参数开关，与仓库自身 CI/Docker 规范完全一致；README 的端口、环境变量、CLI 命令、初始密码获取方式与代码逐项核对一致。

## 测试盲区

- `runServe` 的 `ListenAndServe` 失败路径（端口占用等）无法测试——`log.Fatal` 会杀死测试进程，`serve_test.go` 只能测正常路径；与 dispatch default 分支同属"Fatal 导致不可测"的结构性问题。
- spaHandler 未测目录形路径（如 `/assets/`、`/assets`）与条件请求（304/Range）行为；当前依赖 `fs.Stat` 对带斜杠路径必然失败来兜底回退，这一隐式依赖无测试锁定，重构时易破坏。
- 版本注入链路无端到端测试：version_test 只测默认值拼接，无任何测试断言 ldflags 注入的 Version 能体现在 `/api/v1/version` 响应或启动日志中（CI 与本地构建的差异因此不可感知——本次发现的"CI 镜像恒 dev"正是该盲区的实例）。
- e2e 覆盖了 AI 的 `read`/`reveal` scope，但 `/api/v1/ai/search`（search scope）未在 cmd 层 e2e 中出现（仅在 aiapi 包内测试）；AI 令牌过期/吊销后的行为也未在入口层验证。
- GitHub Actions 两条流水线完全不跑测试（见 P3 最后一条），所有测试覆盖只存在于开发者本机。
- `http.Server` 超时缺失属配置问题无测试可言，但引入超时后需注意：现有 e2e 与 SSE 长连接测试会成为回归敏感点，目前没有针对"超时不误伤 SSE"的用例。
