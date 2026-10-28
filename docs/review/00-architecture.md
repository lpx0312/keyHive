# keyHive 全仓架构与文件清单

> 基线：v1.3.12（commit `0ffd905`），2026-10-28 生成。
> 规模：120 个跟踪文件，约 19,200 行代码（不含 `package-lock.json` 与 `web/dist` 产物）。

## 1. 项目定位

自托管密钥管家：**字段结构完全自由 + 每字段带手写注释（给 AI 看）+ 敏感字段加密分级供 AI 调用**。
单体部署：Go 单二进制（go:embed 内嵌 Vue3 SPA）+ SQLite 单文件，一个容器跑完。

## 2. 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.26.4、chi v5（路由）、modernc.org/sqlite（纯 Go SQLite，无 CGO）、golang.org/x/crypto（argon2id 等）、mark3labs/mcp-go（MCP SDK） |
| 前端 | Vue 3.5 + vue-router 4 + Element Plus 2.9 + markstream-vue（SSE Markdown 流式渲染）、Vite 6、Vitest 5 + coverage-v8 |
| 交付 | 多阶段 Dockerfile（支持 `USE_CN_MIRROR` 国内源）、docker-compose、GitHub Actions 双流水线（release-binary 六平台产物 + docker-image 推 ACR 多架构） |

## 3. 运行形态（一个二进制三种入口）

`cmd/keyhive/main.go` 的 `dispatch()` 按子命令分流：

- **无参 / `serve`** → `runServe()`：HTTP 服务 `:8020`（`buildServer()` 组装，与测试共用）
- **`login/list/search/get/reveal/status/add/edit/totp/export/import/rotate-key`** → `internal/cli`：终端/脚本/CI 客户端
- **`mcp`** → `internal/mcpserver`：stdio MCP Server（kh_status/list/search/get/reveal 五工具）

## 4. 分层架构

```
┌──────────────────────────────────────────────────────────────────┐
│ cmd/keyhive  main.go — dispatch(serve/cli/mcp) + buildServer     │
├──────────────────────────────────────────────────────────────────┤
│ HTTP 层（chi）                                                     │
│  /api/v1     internal/api    人用：登录/条目/分类/模板/令牌/用户/   │
│                              审计/导入导出/密钥轮换/AI 助手设置      │
│  /api/v1/ai  internal/aiapi  AI 用：Bearer 三 scope               │
│                              read(遮蔽)/search/reveal(记审计)      │
│  /*          web/dist        embed SPA（入口 no-cache + assets    │
│                              immutable + gzip）                   │
├──────────────────────────────────────────────────────────────────┤
│ 业务层                                                            │
│  internal/store      条目/分类/模板 CRUD + 加解密编排 + 轮换       │
│  internal/auth       argon2id 密码 / 会话 / AI 令牌 / Bearer 中间件│
│  internal/audit      审计日志（人 + AI 全行为，含 reveal 明文取用） │
│  internal/aichat     AI 录入助手：OpenAI/Anthropic 双协议、SSE 流、 │
│                      15 服务商预设、模型列表获取、测试连接          │
│  internal/templates  32 个内置运维模板（预置字段 + AI 注释）        │
│  internal/importer   Bitwarden/Chrome CSV + keyhive JSON 导入      │
├──────────────────────────────────────────────────────────────────┤
│ 基础层                                                            │
│  internal/db     SQLite 打开/建表迁移/工具函数                     │
│  internal/model  领域模型（Entry/Field/Token/Template/AuditLog…）  │
│  internal/crypto AES-256-GCM + master.key 管理 + key_check 校验   │
│  internal/totp   RFC 6238 动态码（30s，记审计）                    │
│  internal/version ldflags 版本注入                                 │
│  internal/kdbtest e2e 测试夹具（起真实服务共用）                    │
├──────────────────────────────────────────────────────────────────┤
│ 前端 web/src（Vue3 + Element Plus）                                │
│  App.vue 路由壳 / api.ts fetch 封装 / store.ts 全局状态 / ui.ts    │
│  views/ 八页面 / components/ChatDrawer.vue AI 助手抽屉（可全屏）   │
├──────────────────────────────────────────────────────────────────┤
│ 交付                                                              │
│  Dockerfile + docker-compose.yml（多阶段，KEYHIVE_VERSION 可覆盖） │
│  .github/workflows：release-binary（tag→6产物）+ docker-image      │
│  （手动 dispatch：image_tags + architecture，推 ACR）              │
│  skills/keyhive/SKILL.md + .claude-plugin（AI 助手接入规范）       │
└──────────────────────────────────────────────────────────────────┘
```

## 5. 安全模型

1. **存储加密**：敏感字段 AES-256-GCM；主密钥与库文件分离（`master.key` / `KEYHIVE_MASTER_KEY` / `KEYHIVE_KEYFILE` 三来源，优先级 env > keyfile > 数据目录）；库内 `settings.key_check` 校验密钥匹配；`rotate-key` 重加密全库。
2. **认证**：管理员密码 argon2id；会话与 AI 令牌只存哈希（明文令牌仅创建时显示一次）。
3. **AI 分级**：令牌 scope 分 `read`（结构+注释，敏感值遮蔽 `***`）/ `search` / `reveal`（取单字段明文，每次记审计）。
4. **条目级隐身**：`ai_visible=false` 的条目对 AI 令牌完全不存在。
5. **全程审计**：登录成败、增删改、每次敏感值被人查看/AI 取用均落审计表。
6. **已知取舍**：登录防爆破暂缓（内网部署；暴露公网时必做）——见 README 路线图。

## 6. 文件清单（按目录，✅=测试文件）

| 目录 | 行数 | 文件 |
|---|---:|---|
| `cmd/keyhive/` | 652 | main.go、✅main_test.go、✅serve_test.go |
| `internal/api/` | 3393 | api.go、admin.go、rotate.go、users.go、webui.go、aichat_api.go、✅api_test.go、✅api_errors_test.go、✅api_extra_test.go、✅aichat_api_test.go |
| `internal/aichat/` | 2293 | aichat.go、anthropic.go、models.go、presets.go、stream.go、testconn.go、✅aichat_test.go、✅aichat_edge_test.go、✅anthropic_test.go、✅models_test.go、✅stream_test.go、✅testconn_test.go |
| `internal/cli/` | 2103 | cli.go、admincli.go、login.go、✅cli_test.go、✅cli_edge_test.go、✅cli_run_test.go、✅admincli_test.go |
| `internal/store/` | 886 | store.go、✅store_test.go、✅store_extra_test.go |
| `internal/auth/` | 798 | password.go、store.go、middleware.go、✅password_test.go、✅auth_full_test.go |
| `internal/aiapi/` | 531 | aiapi.go、✅aiapi_test.go、✅aiapi_edge_test.go |
| `internal/db/` | 484 | db.go、util.go、✅db_test.go、✅db_extra_test.go |
| `internal/templates/` | 456 | templates.go、✅templates_test.go |
| `internal/mcpserver/` | 366 | mcp.go、✅mcp_test.go |
| `internal/crypto/` | 355 | crypto.go、✅crypto_test.go、✅crypto_edge_test.go |
| `internal/importer/` | 338 | importer.go、keyhive.go、✅importer_test.go、✅keyhive_test.go |
| `internal/model/` | 309 | model.go、✅model_test.go |
| `internal/audit/` | 167 | audit.go、✅audit_test.go |
| `internal/totp/` | 163 | totp.go、✅totp_test.go |
| `internal/kdbtest/` | 81 | fixture.go、✅fixture_test.go |
| `internal/version/` | 50 | version.go、✅version_test.go |
| `web/src/views/` | 3464 | EntriesList.vue、EntryEdit.vue、Login.vue、Settings.vue、Tokens.vue、Users.vue、Audit.vue、Templates.vue + 各自 ✅.test.ts |
| `web/src/`（核心） | 790 | App.vue、main.ts、api.ts、store.ts、ui.ts、base.css、env.d.ts、✅App/api/store/ui.test.ts、test/fixtures.ts |
| `web/src/components/` | 358 | ChatDrawer.vue |
| `web/`（根） | ~100 | web.go（embed）、index.html、package.json、tsconfig.json、vite.config.ts、vitest.config.ts |
| `.github/workflows/` | 449 | release-binary.yml、docker-image.yml |
| 根目录 | ~350 | README.md、go.mod、go.sum、Dockerfile、docker-compose.yml、.gitignore、.zcodeignore |
| `.claude-plugin/` | 35 | plugin.json、marketplace.json |
| `skills/keyhive/` | 111 | SKILL.md |
| `web/dist/` | 构建产物 | favicon.svg、index.html（被 git 跟踪，发版时随功能更新；assets 由 CI/Docker 构建生成） |

## 7. 十路审查任务拆分（本轮执行）

| # | slug | 范围 |
|---|---|---|
| 1 | bootstrap | cmd/keyhive + internal/version + Dockerfile/compose + .github/workflows + .claude-plugin + skills + README |
| 2 | data-foundation | internal/db + internal/model + internal/audit |
| 3 | store-templates | internal/store + internal/templates |
| 4 | crypto-totp | internal/crypto + internal/totp |
| 5 | auth | internal/auth |
| 6 | http-api | internal/api |
| 7 | ai-integration | internal/aichat + internal/aiapi |
| 8 | cli | internal/cli |
| 9 | mcp-importer | internal/mcpserver + internal/importer + internal/kdbtest |
| 10 | frontend | web/src + web 根配置 + web/web.go |
