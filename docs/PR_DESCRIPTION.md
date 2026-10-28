# PR 描述：全仓质量工程轮——安全修复 + 测试补全 + 文档体系（v1.4.0）

## 标题

```
security+test+docs: 全仓质量工程轮——P0 修复 + 25 项修复 + ~100 新测试 + 19 篇模块文档 + CI 测试流水线（v1.4.0）
```

## 摘要

以"10 路并行代码审查 → 缺陷复现红测 → 修复转绿 → 两轮多角色复审 → 文档/发版物料"的流程对全仓做了一次质量工程轮。审查 107 项发现（P0×1 / P1×8 / P2×31 / P3×67），本轮修复 25 项（全部 P0/P1 + 高价值 P2），其余归档进重构路线。后端 17 包全绿（-count=1）、前端 141/141 全绿（四维覆盖率 ≥90% thresholds 保持），前端 dist 已随提交重建。

## 变更分类

### 安全（详见 CHANGELOG「安全」节）

- **P0**：ai-config test/models 端点收紧 admin-only（任意登录用户可外带管理员 LLM Key + SSRF）
- 全站 10MB body 上限（413）、审计 detail 结构化（防注入破坏取证）、5xx 不回显原文
- 空 master.key 拒绝启动、Anthropic 空 baseURL 端点修正、Bitwarden notes 敏感化、CSV 严格化、config.json 0600、服务端遮蔽导出

### 正确性

- 轮换三连修：漏 settings 重加密 / 并发写 TOCTOU（最小写互斥）/ CLI 超时诱发二次轮换
- 坏行隔离、enc:v1: 前缀碰撞、Anthropic 流 error 传播、VerifyPassword panic、CLI flag 解析后仍执行轮换、import 重复登录、MCP IsError

### 测试（先红后绿，16 个 TestBug_ 复现锚点全部转绿）

- 后端新增约 100 用例；aiapi/totp/model/templates 100%；含并发轮换确定性测试与红态验证（回退修复必红）
- 前端 128→141：ChatDrawer 8 用例（原 0 覆盖）、两条 test.fails 缺陷锚点翻正

### 交付与文档

- ci-test.yml（push/PR：vet + go test -race + vitest）、.dockerignore、Dockerfile /app/data 属主、镜像 VERSION 注入、release 权限 job 级、server 超时
- docs/modules/ 19 篇模块文档 + docs/review/ 审查-测试-重构-复审全档案 + CHANGELOG.md + docs/MIGRATION.md

## 测试证据

- `go build ./... && go vet ./...` ✅
- `go test ./... -count=1`：17 包全 ok ✅（含全部 TestBug_ 转绿）
- `cd web && npx vitest run`：141/141 ✅，coverage thresholds ≥90% 全过
- `cd web && npm run build` ✅（dist 已重建并随提交）
- 两轮角色复审独立复跑全部通过（docs/review/04-role-review-*.md）

## 审查过程

1. 10 路按目录并行审查（docs/review/01-review-*.md，107 项发现汇总于 01-review-summary.md）
2. 缺陷全部转复现红测（TestBug_*，16 条）后修复转绿（02-test-report.md）
3. 第一轮 4 角色复审（安全/Go/前端/SRE）→ 4 项收尾修复（含复审发现的 API 层协议盲预填残留）
4. 第二轮 2 角色（独立审计 A- / 发布经理 conditional GO）→ dist 重建 + README 同步 + 测试锚点补齐
5. 未修项全部归档 docs/review/03-refactor-plans.md（8 专项 + 优先级 + 实施节奏建议）

## 行为变更与迁移

10 项用户可见变更（含 1 个新 403 面、导入语义收紧）逐条见 **docs/MIGRATION.md**；无数据库 schema 变更。

## 风险与说明

- ci-test.yml 是新增的 push 触发流水线（此前仓库 CI 仅 tag/手动触发），如不需要可删
- putSetting 密文写的轮换窗口、Audit.vue 竞态等低危残余已归档重构方案（03-refactor-plans.md A1 剩余/F1）
- 登录防爆破维持"暴露公网前必做"的既定决策（README 路线图）

## 发版 checklist（合入后）

1. 打 tag `v1.4.0` → release-binary 流水线自动出 6 平台产物
2. 手动 dispatch docker-image：`image_tags="1.4.0,latest"`、`architecture=all`（现已自动注入 VERSION，镜像版本号将正确显示）
3. `.180:8020` 生产容器更新后强刷浏览器验证：普通用户 Settings 按钮 403 提示、遮蔽导出、AI 助手 Anthropic 空地址
