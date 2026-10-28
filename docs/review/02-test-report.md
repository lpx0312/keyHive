# 测试补全与缺陷修复报告（2026-10-28）

## 1. 基线（修复前）

后端 17 包 `go test ./... -count=1` 全绿，覆盖率：

| 包 | 覆盖率 | | 包 | 覆盖率 |
|---|---:|---|---|---:|
| model | 100.0% | | cli | 88.1% |
| templates | 100.0% | | crypto | 88.1% |
| version | 100.0% | | cmd/keyhive | 86.7% |
| importer | 95.6% | | api | 89.8% |
| aiapi | 95.2% | | auth | 91.7% |
| mcpserver | 95.2% | | aichat | 91.3% |
| totp | 97.2% | | store | 89.4% |
| audit | 94.4% | | db | 81.0% |
| | | | kdbtest | 72.7% |

前端 vitest 128 测全绿（四维 ≥90% thresholds 过）。

## 2. 本轮新增测试（第 3 步）

6 路并行测试 agent + 1 路补齐（db/model/audit/cmd），共新增约 **60+ 用例**：

| 包 | 新增文件 | 绿测 | 红测（TestBug_） |
|---|---|---:|---:|
| crypto | crypto_blind_test.go、crypto_bug_test.go | 8 函数/32 子用例 | 1 |
| totp | totp_blind_test.go | 5 函数/24 子用例 | 0 |
| store | store_edge_test.go、store_bug_test.go | 5 | 3 |
| templates | templates_extra_test.go | 4 | 0 |
| api | api_security_test.go、api_export_test.go | 4(+1) | 3 |
| auth | auth_edge_test.go | 3 | 1 |
| aichat | sse_edge_test.go、anthropic_bug_test.go、endpoint_bug_test.go | 若干 | 2 |
| aiapi | search_edge_test.go、aiapi_bug_test.go | 若干 | 1 |
| cli | bug_test.go | — | 2 |
| importer | edge_test.go、bug_test.go | 5 | 2 |
| mcpserver | edge_test.go、bug_test.go | 2 | 1 |
| web | ChatDrawer.test.ts（新）+ 4 个既有文件扩充 | +13（128→141） | 2（test.fails） |

覆盖重点：加密三来源与畸形输入、GCM 篡改/截断、TOTP 时间与编码边界、五路搜索 unicode、条目复制/批量删除幂等、SSE 畸形流与上游错误、隐身条目全路径、遮蔽版导入拒绝矩阵、MCP 畸形 JSON-RPC、登录畸形请求、ID/分页溢出、非 admin 打 13 个管理端点全 403、Bearer 8 种形态 fail-closed、ChatDrawer 全链路（原 0 覆盖）。

## 3. 缺陷复现 → 修复（第 4 步）

全量首轮失败日志（`go test ./... -count=1`，恰好 16 条红测、9 包 FAIL、其余全 ok）：

```
--- FAIL: TestBug_RevealAuditDetailValidJSON        (internal/aiapi)
--- FAIL: TestBug_AnthropicStreamErrorPropagated    (internal/aichat)
--- FAIL: TestBug_AnthropicEmptyBaseURLUsesAnthropicEndpoint (internal/aichat)
--- FAIL: TestBug_AIConfigTestRequiresAdmin         (internal/api)      ← P0 实锤：普通用户 200 且服务端向其自填 URL 发起请求
--- FAIL: TestBug_MaxBytesRejected                  (internal/api)      ← 11MB body 返回 401（完整解码），无 413
--- FAIL: TestBug_AuditDetailValidJSON              (internal/api)      ← detail={"title":"He said "hi" \ backslash"} 非法 JSON
--- FAIL: TestBug_VerifyPasswordMalformedNoPanic    (internal/auth)     ← t=0/p=0/空段 panic
--- FAIL: TestBug_AdminFlagParseErrorStops          (internal/cli)      ← rotate-key --dry-run 实发 2 个请求且返回 0
--- FAIL: TestBug_ImportSingleLogin                 (internal/cli)      ← 3 条导入登录 3 次
--- FAIL: TestBug_EmptyKeyfileRejected              (internal/crypto)   ← 零字节/纯空白 master.key 被接受
--- FAIL: TestBug_BitwardenNotesAreSecret           (internal/importer) ← notes is_secret=false 且 ai_visible=true
--- FAIL: TestBug_MalformedCSVRejected              (internal/importer) ← 未闭合引号静默解析出脏数据
--- FAIL: TestBug_MCPErrorFlagged                   (internal/mcpserver)← 错误响应 IsError=false
--- FAIL: TestBug_PlaintextWithEncPrefixCorrupted   (internal/store)    ← "enc:v1:" 开头明文原样入库
--- FAIL: TestBug_ListEntriesSurvivesBadRow         (internal/store)    ← 单条坏行拖垮整个列表
--- FAIL: TestBug_RotateMissesSettingsSecret        (internal/store)    ← 轮换漏 settings 中 LLM Key
（前端另有两处 test.fails 锚点：搜索乱序竞态、改密零反馈）
```

修复（6 路并行，全部由红测验证转绿）：

| # | 修复 | 位置 |
|---|---|---|
| 1 | 【P0】ai-config/test、models 端点移入 RequireAdmin 组，普通用户 403 | api.go:118 |
| 2 | 全局 10MB MaxBytesReader，超限 413（Content-Length 预判 + 读错误映射） | api.go:23-37 |
| 3 | 审计 detail 全部改 `audit.Detail()`（json.Marshal），杜绝拼接注入 | audit.go 新增 helper + 18 处调用点 |
| 4 | VerifyPassword 畸形 PHC 串显式校验返回 false，不再 panic | auth/password.go:59 |
| 5 | 5xx 内部错误不再回显原文（writeInternalErr：服务端日志 + 通用文案），22 处 | api 包 |
| 6 | /export?masked=1 服务端遮蔽导出（前端/CLI 传参 + 客户端双保险） | api.go:170 + web + cli |
| 7 | 空/纯空白 master.key 拒绝启动并给出修复指引 | crypto.go:141 |
| 8 | 敏感值一律加密（去掉 IsEncrypted 跳过判断，*** 哨兵语义保持） | store.go:44 |
| 9 | ListEntries 坏行隔离：部分损坏跳过+记日志，全库损坏仍报错（疑似密钥不匹配） | store.go:95,217 |
| 10 | 轮换连同 settings 表 enc:v1:% 值同事务重加密（含 LLM API Key） | store.go:349 |
| 11 | Anthropic 流 error 事件传播为错误（原"空回复+成功"） | anthropic.go:294 |
| 12 | 默认端点按协议分派（anthropic→api.anthropic.com，修 Key 发往错误端点） | aichat.go:47 等 |
| 13 | adminFlags 解析失败退出 2 且零请求（rotate-key 打错 flag 不再执行轮换） | admincli.go:94 |
| 14 | import 登录一次复用会话 | admincli.go:70 |
| 15 | Bitwarden notes 导入 is_secret=true + ai_visible=false | importer.go:114 |
| 16 | CSV LazyQuotes=false，畸形引号报错 | importer.go:62 |
| 17 | MCP 错误结果 NewToolResultError（IsError=true） | mcp.go:136 |
| 18 | config.json 写后 chmod 0600 | login.go:119 |
| 19 | http.Server ReadHeaderTimeout 10s + IdleTimeout 120s（不设 WriteTimeout 保 SSE），runServe 改用配置后的 server | main.go |
| 20 | 交付：.dockerignore（data/ 不再入构建上下文）、/app/data 预建属主、CI 镜像传 VERSION、release workflow 权限降 job 级 | Dockerfile 等 |
| 21 | 前端：改密失败 ElMessage.error、搜索请求序号防乱序覆盖 | Settings.vue、EntriesList.vue |

### 3.1 第一轮角色复审后的收尾修复（4 项）

| # | 修复 | 位置 |
|---|---|---|
| 22 | API 层 currentLLMConfig 不再协议盲预填 baseURL（aichat 层按协议分派真正生效；存什么回什么，不再持久化伪造端点） | aichat_api.go:31 |
| 23 | body 限制盖住 /api/v1/ai 子树（LimitBody 提到顶层 router） | api.go:31、main.go:122 |
| 24 | aiapi 两处 5xx 内部错误不再回显原文 | aiapi.go:35 |
| 25 | 轮换/写路径最小写互斥（writeMu；并发 Create + Rotate 确定性测试锚定） | store.go:34 |

新增 3 个测试文件：aichat_api_protocol_test.go、limitbody_ai_test.go、store_concurrent_test.go（均先做红态验证：回退修复必红）。

## 4. 最终验证

- `go test ./... -count=1`：**17 包全 ok，退出码 0**
- `cd web && npx vitest run`：**141/141 全过，退出码 0**（两条缺陷锚点翻正），coverage thresholds ≥90% 保持（93.5/90.9/90.8/96.8）
- `go build ./...`、`go vet`（相关包）通过

修复后全量覆盖率（`go test ./... -cover`，修复代码 + 新测试就位后）：

| 包 | 前 → 后 | | 包 | 前 → 后 |
|---|---|---|---|---|
| aiapi | 95.2 → **100.0%** | | auth | 91.7 → 91.8% |
| totp | 97.2 → **100.0%** | | api | 89.8 → 90.0% |
| aichat | 91.3 → 94.0% | | crypto | 88.1 → **91.8%** |
| importer | 95.6 → 97.1% | | cli | 88.1 → 87.1%（新增修复路径待补锚点） |
| mcpserver | 95.2 → 95.2% | | store | 89.4 → 86.6%（轮换新逻辑，见盲区） |
| audit | 94.4 → 95.0%* | | db | 81.0 → 81.0% |
| cmd/keyhive | 86.7 → 89.0%* | | kdbtest | 72.7%（夹具包，不追） |

\* 来自补齐批次单包实测。db/model/audit/cmd 补齐批次另增 16 个测试函数（Open 幂等/seed 形状、Validate 边界、Detail() 与 limit 封顶、dispatch 表驱动与 SPA 缓存头）。

## 5. 遗留测试盲区（进重构方案）

db seed 中断恢复、审计写入失败路径、登录时序、轮换并发写互斥（本轮修了 settings 一致性，进程级写锁进重构方案）、ChatDrawer 覆盖率统计口径（config exclude）、`-race` 本机无 cgo 无法跑（CI Linux 可跑）。
