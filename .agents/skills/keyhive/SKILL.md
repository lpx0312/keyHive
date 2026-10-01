---
name: keyhive
description: 从 keyHive 密钥管家查询/取用凭据。任务需要任何凭据时使用：docker login / 镜像仓库认证、数据库连接串、云平台 AK/SK、API key/token、SSH 登录、SMTP 发信、Web 控制台账号、VPN、代理、两步验证码等；用户提到 keyHive / 密钥管家 / "查一下我的密钥" 时也使用。提供搜索条目、查看字段结构与注释（敏感值遮蔽）、reveal 取单字段明文、totp 生成动态码、admin 通道录入/更新条目。
---

# keyHive 密钥管家

keyHive 是密钥管家服务（默认 http://localhost:8020，Docker 容器 keyhive）。库里每条目目字段结构自由、每个字段带用户手写的注释——**先读注释理解字段含义，再决定用什么**。

## 命令

二进制在本仓库根目录（相对本 skill 为 `../../keyhive.exe`；本机绝对路径 `D:/Users/Desktop/AGENT-CODE/keyHive/keyhive.exe`，按仓库实际位置调整）：

```bash
KH="D:/Users/Desktop/AGENT-CODE/keyHive/keyhive.exe"

# 只读组（AI 令牌，日常用）
"$KH" status                              # 服务连通性/令牌配置检查
"$KH" list [--category <分类>] [--stale N]  # 列条目（遮蔽+注释）；--stale 只看超 N 天未更新
"$KH" search <关键词> [--stale N]           # 搜索（标题/说明/分类，中英文均可）
"$KH" get <id>                            # 单条详情（遮蔽）
"$KH" reveal <id> <字段名>                 # 取单字段明文（记审计）
"$KH" totp <id> [--field totp_secret]     # 生成两步验证 6 位动态码（密钥不进上下文，记审计）

# admin 组（需要管理员密码：--pass 或环境变量 KEYHIVE_ADMIN_PASS）
"$KH" add --file <条目.json>              # 录入条目（--file 留空则读 stdin）
"$KH" edit <id> <field>=<value> [...]     # 更新字段（轮换后更新库值；见下方规则）
"$KH" import --file <csv> --format bitwarden|chrome [--dry-run]  # 批量导入
"$KH" export [--masked]                   # 全库导出（--masked 敏感值遮蔽）
"$KH" rotate-key                          # 主密钥轮换（重加密全部条目）
```

reveal 返回 JSON（entry_id/entry_title/key/description/value），stdout 可直接管道取裸值，明文全程不进上下文：
```bash
"$KH" reveal 1 password | jq -r .value | docker login <registry> -u <user> --password-stdin
```

edit 规则：field 限已有字段（或 title/category/description/ai_visible）；**未提及的敏感字段保留原值**（以遮蔽值回传，服务端处理，零明文暴露）；把敏感字段显式设为 `***` 会被拒绝。

## 工作流（必须遵守）

1. **先查库，别问用户要凭据**。任务需要任何凭据时，先 `search` / `list` 查 keyHive，命中就不用问用户。
2. **遮蔽优先**。列表/详情里敏感值是 `***`，但字段名、类型、`description`（用户手写注释）完整。仅凭这些就该完成判断，**不需要明文就不 reveal**。
3. **reveal 最小化**。确需明文（如 docker login）只取当下必需字段：
   - 明文只注入执行环境（export、命令参数、临时文件用后删），**绝不**写进源代码/配置文件/git/对话正文
   - 汇报说"已从 keyHive 取用 xxx 字段"即可，不复述值本身（用户明确要求展示除外）
4. **注释即权威**。字段怎么用以 `description` 为准，不凭字段名猜。
5. **两步验证优先用 totp**：等效"零明文 reveal"（密钥经 reveal 后本地算码，不进上下文），多字段成套取用时也尽快用完不滞留。
6. **查不到时先区分两种情况**：404/搜不到可能是库里没有，也可能是条目设置了 **AI 不可见**（对令牌完全隐身，仅人工 Web 可见）——后者提示用户"该条目可能对 AI 隐身，如需我访问请在 Web UI 打开 AI 可见"。确认没有的：换短词/中英文重试 → 如实告知并建议补录；若已配 KEYHIVE_ADMIN_PASS 可直接 `add` 代录。轮换场景（`--stale` 或 stderr 出现 `⚠️ N 条超 90 天未更新` 提醒时）：建议用户轮换密码后用 `edit` 更新库值。

## 故障处理

- 服务不可达 → 提示：`docker start keyhive`（或 `cd keyHive 目录 && docker compose up -d`）
- reveal 403 或提示令牌未配置 → 指引 Web UI「AI 令牌」页创建/更新（read/search/reveal）；**totp 需要 token_read 和 token_reveal 同时配置**
- admin 命令报"未提供管理员密码" → 需要用户以 `KEYHIVE_ADMIN_PASS` 环境变量或 `--pass` 提供（AI 不要在对话记录里留存该密码）
- 搜不到 → 见工作流第 6 条

## 配置

`~/.keyhive/config.json`（令牌由管理员在 Web UI「AI 令牌」页创建，明文仅显示一次）：

```json
{
  "base_url": "http://localhost:8020",
  "token_read": "kh_...",
  "token_reveal": "kh_..."
}
```

环境变量（对所有命令与 MCP 生效）：
- `KEYHIVE_CONFIG`：覆盖配置文件路径
- `KEYHIVE_BASE_URL`：临时指向其他实例（如 `http://192.168.0.10:8020`），不改配置文件
- `KEYHIVE_ADMIN_PASS`：admin 组命令的密码（避免进 shell 历史，优于 --pass）

每次 reveal / totp / admin 写操作都记审计（哪个令牌、何时、动了哪条），用户可在 Web UI 审计页回查。

## MCP 接入（可选的另一种方式）

`keyhive mcp` 以 stdio MCP server 运行（kh_status / kh_list / kh_search / kh_get / kh_reveal，与 CLI 同一套配置；**没有 totp/admin 工具，需要时仍走 CLI**）。配置示例：

```json
{
  "mcpServers": {
    "keyhive": {
      "command": "D:/Users/Desktop/AGENT-CODE/keyHive/keyhive.exe",
      "args": ["mcp"]
    }
  }
}
```

即使配了 MCP，本 skill 的行为规范（遮蔽优先、最小 reveal、不落盘）同样适用。
