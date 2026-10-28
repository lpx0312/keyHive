# 模块文档索引

> 全部基于 2026-10-28 修复后的工作区代码撰写；全仓架构见 [../review/00-architecture.md](../review/00-architecture.md)。

## 基础层（internal）

| 模块 | 文档 | 一句话 |
|---|---|---|
| 入口 | [cmd-keyhive.md](cmd-keyhive.md) | dispatch 三入口 + buildServer 组装 + SPA 缓存策略 + server 超时 |
| 版本 | [version.md](version.md) | ldflags 版本注入 |
| 数据库 | [db.md](db.md) | SQLite 打开/建表迁移/seed/单连接池 |
| 领域模型 | [model.md](model.md) | Entry/Field/Token 等模型与 Validate 契约 |
| 审计 | [audit.md](audit.md) | Log/Count/List + Detail() 安全构造 |
| 加密 | [crypto.md](crypto.md) | AES-256-GCM、enc:v1: 格式、主密钥三来源、key_check |
| TOTP | [totp.md](totp.md) | RFC 6238 动态码 |

## 业务层（internal）

| 模块 | 文档 | 一句话 |
|---|---|---|
| 存储 | [store.md](store.md) | 条目/分类/模板 CRUD + 加解密编排 + 轮换（含 settings） |
| 模板 | [templates.md](templates.md) | 内置运维模板与 builtin 保护 |
| 认证 | [auth.md](auth.md) | argon2id/会话/AI 令牌/中间件 |
| HTTP API | [api.md](api.md) | 37 端点路由表、错误纪律、遮蔽导出 |
| AI 助手 | [aichat.md](aichat.md) | OpenAI/Anthropic 双协议、SSE、预设 |
| AI API | [aiapi.md](aiapi.md) | read/search/reveal 三 scope 与隐身 |
| CLI | [cli.md](cli.md) | 子命令、config、admin 会话复用 |
| 导入器 | [importer.md](importer.md) | CSV/JSON 导入与遮蔽拒绝 |
| MCP | [mcpserver.md](mcpserver.md) | stdio 五工具 |
| 测试夹具 | [kdbtest.md](kdbtest.md) | e2e 起真实服务 |

## 前端与交付

| 模块 | 文档 | 一句话 |
|---|---|---|
| 前端 | [web-frontend.md](web-frontend.md) | 页面/契约/安全不变量/测试体系 |
| 交付 | [delivery.md](delivery.md) | Docker/CI/版本链路/发版 checklist |
