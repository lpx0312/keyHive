# Changelog

本文件记录 keyHive 各版本变更。格式：**安全 / 新增 / 修复 / 交付 / 文档与测试**。

## [1.4.0] — 未发布（本轮工作区，待打 tag）

> 全仓质量工程轮：10 路并行代码审查（107 项发现）→ 25 项修复 + 约 100 个新增测试 → 两轮多角色复审（安全架构师 / Go 工程 / 前端 / SRE / 独立审计 / 发布经理）。过程档案见 `docs/review/`。

### 安全（Security）

- **【P0】`/api/v1/ai-config/test`、`/api/v1/ai-config/models` 收紧为管理员专用**：此前任意登录用户可借"空 Key 回退已保存管理员 Key"让服务器把真实 LLM Key 发往自填 URL（Key 外带 + SSRF 读原语）（api.go、aichat_api.go）
- 全站请求体 10MB 上限（含 `/api/v1/ai` 子树），超限 413，未认证登录端点不再可超大 JSON 耗内存
- 审计 detail 改 `audit.Detail()` 结构化构造（json.Marshal）：标题/字段名含引号不再破坏审计记录取证契约
- 内部 5xx 错误不再回显原文（api 22 处 + aiapi 2 处：服务端日志留全量、客户端只见通用文案）
- 空/纯空白 master.key 拒绝启动（此前静默退化为 SHA-256("") 公开常量，加密形同虚设）
- Anthropic 协议 + 空 baseURL 默认端点修正为官方 `api.anthropic.com`（此前会把用户 Anthropic Key 发往 OpenAI 端点；API 层协议盲预填一并移除，不再把伪造端点持久化）
- Bitwarden 导入的 notes 字段改为敏感 + AI 不可见（恢复码等不再明文直达 AI 上下文）
- CSV 导入严格化：未闭合引号等畸形输入报错拒绝（此前 LazyQuotes 静默吞行产生脏数据）
- CLI config.json 写后强制 chmod 0600（此前已存在的 0644 文件合并写入后权限不变）
- 遮蔽导出改服务端完成：`GET /api/v1/export?masked=1`（明文不再出进程；前端/CLI 传参 + 客户端双保险）

### 修复（Fixed）

- 主密钥轮换漏重加密 settings 中同 Cipher 加密的 LLM API Key → 轮换后 AI 助手失效（现连同条目、key_check 同事务重加密）
- 轮换与并发写 TOCTOU：轮换窗口内新建/更新条目会留下旧钥密文永久不可解密（新增 store 写互斥，并发轮换确定性测试锚定）
- 库中单条坏行（解密失败）拖垮整个条目列表/导出 → 部分损坏跳过并记日志，全库损坏仍整体报错（疑似密钥不匹配场景）
- 字段值本身以 `enc:v1:` 开头被误判"已加密"原样入库制造坏行 → 敏感值一律加密
- Anthropic 流式 error 事件被吞（表现为"空回复+成功"）→ 传播为错误
- `VerifyPassword` 对畸形 PHC 串 panic → 显式校验返回 false
- CLI `rotate-key --dry-run` 等打错 flag 后仍会真实执行轮换 → 解析失败退出 2 且零请求
- CLI import 逐条重复 admin 登录（N 条 = N 次 argon2id + N 会话 + N 审计）→ 登录一次复用
- MCP 工具错误结果补 `IsError` 标记（AI 客户端可区分成败）
- 前端：修改密码失败零反馈（补错误提示）；搜索慢响应乱序覆盖（请求序号防护）

### 交付（Build / CI）

- 新增 `ci-test.yml`：push(main)/PR 触发 go vet + `go test -race` + vitest（此前测试仅本机跑、发版流水线不跑测试）
- 新增 `.dockerignore`：本地 `data/`（含 master.key）、node_modules 等不再进入构建上下文
- Dockerfile `/app/data` 预创建属主：非 root + bind mount 首启不再 EACCES 崩溃循环
- docker-image CI 注入 `--build-arg VERSION`：ACR 镜像版本号不再恒为 dev
- release-binary 权限收敛到 job 级（构建 job 无需写权限）
- http.Server 增加 ReadHeaderTimeout 10s / IdleTimeout 120s（防 slowloris；不设 WriteTimeout 以保 SSE）

### 文档与测试（Docs / Test）

- 后端新增约 100 个测试用例（含 16 个缺陷复现红测，全部先红后绿）；aiapi/totp/model/templates 达 100%，crypto 88.1→91.8%，aichat 91.3→94.0%
- 前端 128→141 测（ChatDrawer 从 0 覆盖到 8 用例；两条 `test.fails` 缺陷锚点翻正）
- 新增 `docs/modules/` 19 篇模块文档、`docs/review/` 全仓审查/测试/重构方案/两轮复审档案、本 CHANGELOG 与 `docs/MIGRATION.md` 迁移指南

## 历史版本

| 版本 | 日期 | 摘要 |
|---|---|---|
| 1.3.12 | 2026-10-27 | 全站 UI 统一模板页设计语言 + 前端测试全覆盖 128 测 |
| 1.3.11 | 2026-10-27 | 录入模板界面重设计（分组卡片网格 + 模板选择页统一） |
| 1.3.10 | 2026-10-25 | 测试连接结果完善（实测耗时 + 协议显示 + 中文截断乱码修复） |
| 1.3.9 | 2026-10-25 | AI 助手多协议接入（OpenAI + Anthropic + 15 服务商预设） |
| 1.3.8 | 2026-10-25 | AI 助手抽屉全屏 |
| 1.3.7 | 2026-10-24 | 登录提速（路由内跳转 + 静态缓存/gzip） |
| 1.3.6 | 2026-10-09 | 修复搜索无结果整页崩溃（JSON null 击穿前端） |
| 1.3.5 | 2026-10-09 | 侧栏固定 + 审计后端真分页 |
| 1.3.4 | 2026-10-09 | AI 悬浮球可拖动/可隐藏 |
| 1.3.3 | 2026-10-09 | 条目列表分页 |
| 1.3.2 | 2026-10-04 | 条目复制 |
| 1.3.1 | 2026-10-03 | URL 列 + 多标签 + 五路搜索 |
| 1.3.0 | 2026-10-03 | 条目批量删除 + 模板增删改 |
| 1.2.9 | 2026-10-03 | 导入自动预览 |
| 1.2.8 | 2026-10-03 | 切格式清文件状态防欺骗 |
| 1.2.7 | 2026-10-03 | 原生 JSON 导入 + 遮蔽版拒绝 |
| 1.2.6 | 2026-10-03 | HTTP 局域网复制按钮降级修复 |
| 1.2.5 | 2026-10-03 | `keyhive login` 子命令 + module path 定型 |
| ≤1.2.4 | — | 基础能力建设：条目/加密/令牌/审计/CLI/MCP/TOTP/轮换提醒/导入导出/密钥轮换/版本注入（明细见 git log） |
