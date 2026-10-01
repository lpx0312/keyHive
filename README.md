# keyHive 🍯 密钥管家

自托管密钥管家：**字段结构完全自由 + 每个字段带手写注释（给 AI 看）+ 敏感字段加密分级供 AI 调用**。

单体部署：Go 单二进制（内嵌 Vue3 SPA）+ SQLite 单文件，一个容器跑完。

## 核心特性

- **字段任意**：每条目目字段数量不限、名称自定，存为 JSON（不用为每类软件建表）
- **三层说明**：条目说明 + 每字段"给 AI 的说明"（录入时软必填）+ 32 个内置运维模板预置注释
- **AI 分级访问**：Bearer 令牌 scope 分 `read`（结构+注释，敏感值遮蔽 `***`）/ `search` / `reveal`（取单字段明文）
- **条目级隐身**：`ai_visible=false` 的条目（root 密码等）对 AI 令牌完全不存在
- **全程审计**：登录成败、增删改、每次敏感值被查看/取用（人与 AI）都记录
- **存储加密**：敏感字段 AES-256-GCM；主密钥与环境/库文件分离；密码 argon2id；令牌/会话只存哈希

## 快速开始

### Docker（推荐）

```bash
docker compose up -d --build
# 国内网络：docker compose build --build-arg USE_CN_MIRROR=1 && docker compose up -d
```

打开 `http://<主机>:8020`。**首次启动的初始密码打印在容器日志里**：

```bash
docker logs keyhive 2>&1 | grep 初始密码
```

登录后立即在「设置」改密码。

### 裸机

```bash
# 前端（已预构建 web/dist 可跳过）
cd web && npm ci && npm run build && cd ..
go build -o keyhive ./cmd/keyhive
KEYHIVE_DATA=./data ./keyhive
```

## AI 接入

在 Web UI「AI 令牌」页创建令牌（明文只显示一次）：

```bash
TOKEN="kh_xxx"
BASE="http://<主机>:8020/api/v1/ai"

# 1. 列出 AI 可见条目（含字段结构与注释，敏感值遮蔽）
curl -H "Authorization: Bearer $TOKEN" $BASE/entries

# 2. 搜索
curl -H "Authorization: Bearer $TOKEN" "$BASE/search?q=SWR"

# 3. 取某个敏感字段的明文（需 reveal scope，记审计）
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"field":"password"}' $BASE/entries/3/reveal
```

返回示例（AI 直接可读）：

```json
{
  "title": "华为SWR-杭州-生产",
  "description": "华为云容器镜像服务 SWR 杭州区域，生产环境 CI 拉取镜像使用",
  "fields": [
    {"key": "registry", "is_secret": false, "value": "swr.cn-east-3.myhuaweicloud.com",
     "description": "SWR 拉取地址，docker login/pull 使用"},
    {"key": "password", "is_secret": true, "value": "***",
     "description": "docker login 密码（可用华为云登录密钥）"}
  ]
}
```

### 安全建议

- 日常给 AI 只发 `read`(+`search`)；需要执行 docker login 等任务时临时发带 `reveal` 的短期令牌，用完吊销
- 核心凭据（root/主密钥/银行）设 `AI 不可见`，仅人工登录查看
- 密钥一旦 reveal 给 AI 即进入模型上下文，请按敏感级别决定是否走 AI 通道
- 服务仅部署内网；如需暴露请套 HTTPS 反代

## CLI（终端 / 脚本 / CI 取密钥）

同一个二进制内置客户端子命令，配置读 `~/.keyhive/config.json`（`KEYHIVE_CONFIG` 环境变量可覆盖）：

```bash
keyhive status                    # 检查服务/配置/令牌
keyhive list [--category mysql]   # 列出条目（遮蔽，含注释）
keyhive search "SWR"              # 搜索（中文/英文均可）
keyhive get 3                     # 条目详情（遮蔽）
keyhive reveal 3 password         # 取单字段明文（记审计）
keyhive totp 5                    # 生成两步验证 6 位动态码（30 秒有效，记审计）
keyhive add --file entry.json     # 录入条目（admin 登录；密码用 --pass 或 KEYHIVE_ADMIN_PASS 环境变量传入，避免进 shell 历史）
keyhive list --stale 90           # 只看超 90 天未更新的条目（密码轮换提醒）
keyhive import --file bitwarden.csv --format bitwarden [--dry-run]  # 从 Bitwarden/Chrome CSV 批量导入
keyhive export [--masked]         # 全库导出（admin；--masked 输出遮蔽版；明文导出记审计）
keyhive rotate-key                # 主密钥轮换：重加密全部条目+更新 key_check（admin，记审计）
```

CI 示例（GitHub Actions 中取密码做 docker login）：

```yaml
- run: |
    echo "${{ secrets.KEYHIVE_TOKEN }}" > ~/.keyhive/config.json  # 或用环境变量组装
    keyhive reveal 3 password | docker login swr.cn-east-3.myhuaweicloud.com -u ci-bot --password-stdin
```

## MCP 接入（AI 客户端原生工具）

`keyhive mcp` 以 stdio MCP server 运行，暴露 kh_status / kh_list / kh_search / kh_get / kh_reveal 五个工具（与 CLI 同一配置、同一套审计）。ZCode / Claude / Cursor 的 MCP 配置：

```json
{
  "mcpServers": {
    "keyhive": {
      "command": "/path/to/keyhive",
      "args": ["mcp"]
    }
  }
}
```

## 备份

数据全在 `data/` 卷（或 `KEYHIVE_DATA` 目录）：`keyhive.db` + `master.key`。**两者一起备份**，拷贝文件即完成。

## 配置

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `KEYHIVE_ADDR` | `:8020` | 监听地址 |
| `KEYHIVE_DATA` | `./data` | 数据目录（库 + 密钥） |
| `KEYHIVE_MASTER_KEY` | 自动生成 | 主密钥（任意字符串，SHA-256 派生）；与数据目录分离存放更安全 |
| `KEYHIVE_KEYFILE` | - | 主密钥文件路径（优先于数据目录内 master.key） |

## 开发

```bash
go test ./...          # 后端测试（crypto/auth/store/aiapi）
cd web && npm run dev  # 前端开发服（代理 /api 到 :8020）
```

## 路线图（未做）

MCP server 子命令（`keyhive mcp` stdio）、导入导出、密钥轮换。
