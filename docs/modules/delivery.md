# 交付链路（Docker / CI / 插件 / 文档）

> 一个 Go 单二进制（go:embed 前端）+ SQLite，三种交付形态（compose / docker run / 裸机二进制），
> 两条 GHA 流水线（release-binary 发 Release、docker-image 推镜像）+ 一份 Claude 插件资产。

## 部署形态（compose / docker run ACR / 裸机）

- **Docker Compose（源码本地构建）**：`docker compose up -d --build`。`docker-compose.yml` 固定
  `USE_CN_MIRROR: "1"`（npm→npmmirror、Go→goproxy.cn）与 `VERSION: ${KEYHIVE_VERSION:-dev}`；
  端口 `8020:8020`，卷 `./data:/app/data`，`restart: unless-stopped`，`TZ=Asia/Shanghai`。
- **Docker Run（群晖 NAS 等免构建场景）**：镜像发布在阿里云 ACR 多架构
  `registry.cn-hangzhou.aliyuncs.com/lpx03/keyhive:latest`（amd64/arm64）；
  `-v /volume1/docker/keyhive:/app/data`，升级 = `docker pull` + 删容器重跑，回滚换固定版本号 tag；
  不开 SSH 的群晖用 `docker save` 导 tar 后 Container Manager 导入。
- **裸机**：`cd web && npm ci && npm run build`（web/dist 就绪可跳过）→ `go build -o keyhive ./cmd/keyhive`
  → `KEYHIVE_DATA=./data ./keyhive`。CLI 客户端可 `go install github.com/lpx0312/keyHive/cmd/keyhive@latest`
  或从 Releases 下载对应平台包。
- 首次启动初始密码打印在服务日志：`docker logs keyhive 2>&1 | grep 初始密码`。

| 环境变量（服务端） | 默认 | 说明 |
|---|---|---|
| `KEYHIVE_ADDR` | `:8020` | 监听地址 |
| `KEYHIVE_DATA` | `./data` | 数据目录（keyhive.db + master.key） |
| `KEYHIVE_MASTER_KEY` | 自动生成 | 主密钥（任意字符串，SHA-256 派生）；与数据目录分离更安全 |
| `KEYHIVE_KEYFILE` | - | 主密钥文件路径（优先于数据目录内 master.key） |

客户端侧另有：`KEYHIVE_CONFIG`（覆盖 `~/.keyhive/config.json` 路径）、`KEYHIVE_BASE_URL`（临时指向其他实例）、
`KEYHIVE_ADMIN_PASS`（admin 组命令密码，避免进 shell 历史）。

## Dockerfile（多阶段 / 非 root / 国内源开关）

三阶段结构（`Dockerfile`）：

1. **web**（`node:24-alpine`）：`ARG USE_CN_MIRROR=0`；为 1 时 `npm config set registry https://registry.npmmirror.com`；
   `npm ci` → `npm run build` 产出 `dist/`（只用公网镜像源，无内网地址，GHA 可跑）。
2. **build**（`golang:1.26-alpine`）：`ARG VERSION=dev`；`CGO_ENABLED=0 GOFLAGS=-trimpath`；
   国内源切 `GOPROXY=https://goproxy.cn,direct`；`COPY --from=web /src/web/dist` 注入前端；
   `go build -ldflags="-s -w -X github.com/lpx0312/keyHive/internal/version.Version=${VERSION}"`。
3. **运行镜像**（`alpine:3.21`）：`adduser -D -H -u 1000 keyhive`（非 root，UID/GID 1000）+
   `apk add ca-certificates tzdata`；**构建期预创建 `/app/data` 并 `chown 1000:1000`**——否则非 root
   首次以匿名卷/命名卷启动时写库 EACCES；`USER keyhive`；`ENV KEYHIVE_DATA=/app/data KEYHIVE_ADDR=:8020`；
   `VOLUME ["/app/data"]`、`EXPOSE 8020`、`ENTRYPOINT ["/app/keyhive"]`。

`.dockerignore` 要点（构建上下文最小化，镜像内自行重建前端）：

- **`data/` 严禁入上下文**（含 master.key！）、`tmpdata/`、`*.log`
- 本地产物：`*.jar`、`*.exe`、`/keyhive` 二进制
- 前端可重建项：`web/node_modules`、`web/dist`、`web/coverage`
- 与镜像无关：`.git`、`.github`、`.zcode`、`.claude-plugin`、`docs/`、`skills/`、`*.md`、`docker-compose.yml`

## CI 流水线（.github/workflows/）

### release-binary.yml（Build Binary & Release）

- **触发**：手动 `workflow_dispatch`（输入 `version`、`use_cn_mirror`）或推送 `v*` tag；**不随普通 push 触发**。
- **权限模型**：workflow 级不授权（最小化），权限全部降到 job 级——`build-frontend` / `build-binary`
  仅 `contents: read`，`release` 是唯一拿 `contents: write` 的 job。
- 三个 job：
  1. `build-frontend`：Node 22 构建一次 `web/dist`，artifact `web-dist` 共享给各平台；
  2. `build-binary`：矩阵 `fail-fast: false` 交叉编译 5 组合（linux amd64/arm64、darwin amd64/arm64、
     windows amd64）；CGO_ENABLED=0 纯静态无需分架构 runner；`Resolve Version` 步骤统一 tag 触发取
     `GITHUB_REF_NAME`、手动触发取输入值；windows 打 zip、其余打 tar.gz，artifact `keyhive-binary-*`；
  3. `release`：合并全部产物，`sha256sum` 生成 `keyhive-v<版本>.sha256sum.txt`，
     `softprops/action-gh-release@v2` 挂同一 tag（`generate_release_notes: true`、`make_latest: true`）。
- Release 共 6 个资产：5 个平台压缩包 + 1 个 sha256 清单。

### docker-image.yml（Build Docker Image）

- **触发**：仅 `workflow_dispatch`（现状：**不随 push/tag 自动跑，推镜像必须手动 dispatch**）；
  顶层 `permissions: contents: read`。
- dispatch 参数（inputs）：
  - `target_registry`（choice）：ACR `registry.cn-hangzhou.aliyuncs.com/lpx03`（默认）/ 华为 SWR
    `swr.cn-east-4.myhuaweicloud.com/lpx03` / `docker.io/sktill` / 腾讯 CCR `ccr.ccs.tencentyun.com/lpx03`
  - `image_name`（默认 `keyhive`）、`image_tags`（逗号分隔多个，如 `"1.1.0,latest"`）
  - `architecture`：`all`（默认，双架构并行）/ `amd64` / `arm64`
  - `use_cn_mirror`（runner 在国外，保持 false）
- job 结构：`build-amd64`（ubuntu-latest）与 `build-arm64`（**ubuntu-24.04-arm 原生 runner，不走 QEMU**；
  不用 matrix + 动态 runs-on 是因实测触发 startup_failure，故拆独立 job 静态 runs-on）；
  `merge-manifest` needs 两者，`docker/setup-buildx-action` + `docker buildx imagetools create` 合并。
- tag 规则：all 模式各架构推 `<tag>-amd64` / `<tag>-arm64` 后缀单架构 tag，再合并出不带后缀的多架构 `<tag>`；
  单架构模式直接推不带后缀 tag。
- **VERSION 注入**：从 `image_tags` 取第一个非 latest 的 tag 去 `v` 前缀（全 latest 回退 `dev`），
  `docker build --build-arg VERSION="${VER}"` 传入，与 compose 的 `VERSION: ${KEYHIVE_VERSION:-dev}` 传法对齐；
  `use_cn_mirror=true` 时追加 `--build-arg USE_CN_MIRROR=1`。
- 凭据：仓库 vars `*_REGISTRY_USER` + secrets `*_REGISTRY_PASSWORD`（ALIYUN/HUAWEI/DOCKERHUB/TENCENT 四套，
  只推 ACR 时配 ALIYUN_* 两项即可）；`docker login --password-stdin`。

## 版本注入链路（ldflags → version.go → 三处展示）

```
-ldflags "-s -w -X github.com/lpx0312/keyHive/internal/version.Version=<值>"
    → internal/version/version.go（Version 默认 "dev"、Commit 默认 "none"，String() 附 runtime.Version()）
```

- 注入端：Dockerfile build 阶段（`ARG VERSION=dev`）、release-binary.yml（注入**带 v** 的 tag，如 v1.3.12）、
  docker-image.yml（从 image_tags 推导**不带 v** 的版本）、compose（`KEYHIVE_VERSION` 覆盖）。
- 消费端：启动日志 `keyHive %s | 主密钥来源: ...`（main.go buildServer）；`keyhive version` 子命令；
  `GET /api/v1/version` → Web 设置页页脚「keyHive {{version}}」。
- 注意：`go install` 与裸机 `go build`（不带 ldflags）显示 `dev`，属预期非缺陷。

## AI 助手接入资产

- **skills/keyhive/SKILL.md**：frontmatter `name: keyhive` + description 定义自动触发场景（任务需要任何
  凭据时）。核心内容：假定 CLI 已装且已连实例；只读命令组（status/list/search/get/reveal/totp）与
  admin 组（add/edit/import/export/rotate-key，凭 `KEYHIVE_ADMIN_PASS`）；六条工作流——先查库别问用户要凭据、
  遮蔽优先（凭字段名 + description 注释判断，不需要明文不 reveal）、reveal 最小化且明文不落盘、
  注释即权威、两步验证优先 totp、查不到先区分「库里没有 vs 条目 AI 不可见」。
- **.claude-plugin/**：`plugin.json`（name keyhive、version 1.2.4、`"skills": "skills"` 指向 skill 目录）+
  `marketplace.json`（插件市场清单，`source: "./"`）——Claude 插件市场可直接安装。
- MCP 通道：`keyhive mcp` stdio server（kh_status/kh_list/kh_search/kh_get/kh_reveal 五工具，无 totp/admin，
  与 CLI 同一配置同一套审计）。

## 发版 checklist（含已知坑）

1. 后端改动：`go test ./...` 全绿；前端改动：`cd web && npm run test`（阈值 90）。
2. **改前端必须 `npm run build` 后重新编译并重启服务**——`go:embed` 是编译期快照，只改源码不 rebuild 无效；
   index.html 已设 no-cache、assets 按文件名 hash 长缓存，正常发版浏览器不会拿旧资源。
3. 打 tag `v<版本>` 推送（或 Actions 手动 dispatch release-binary 填 version）→ Release 出 6 资产。
4. **镜像流水线须手动 dispatch**（不随 tag 触发）：Actions → Build Docker Image，`image_tags` 填
   `"<版本>,latest"`、architecture 保持 all；VERSION 会自动取 `<版本>` 注入。
5. compose 部署侧同步：`KEYHIVE_VERSION=<版本> docker compose up -d --build`。
6. 同步 bump `.claude-plugin/plugin.json` 与 `marketplace.json` 的 `version` 字段（当前 1.2.4）。
7. README 快速开始示例与群晖升级指引复核（latest 拉取 + 固定版本回滚路径）。

其他已知坑：`image_tags` 全填 latest 时镜像 VERSION 落回 `dev`（设置页会显示 dev）；
`use_cn_mirror` 在 GHA runner 保持 false（runner 在国外，国内源反而慢）；单架构模式推的
不带后缀 tag 会直接指向该架构镜像，与 all 模式合并出的同名多架构 tag 语义不同，慎混用。
