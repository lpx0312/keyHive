# 角色复审 R2 — 发布经理（发版就绪度 + 行为变更清点，2026-10-28）

> 复审人视角：发布经理。对象：基线 v1.3.12（tag，提交 0ffd905）以来的全部工作区未提交改动（30 个修改文件 +30 个新增文件，+620/-155 行）。
> 方法：`git status` / `git diff` 全量走读 + 前端调用链核对 + README 逐节比对 + r1 四份复审 ⚠️/❌ 项逐条闭环核对 + dist 构建时效实证。只读审查，未改任何业务文件。
> 产出定位：本文「行为变更清单」即 CHANGELOG / 迁移指南 / PR 描述的直接素材，每条均带 file:line 证据。

## 行为变更清单（分类表格）

### A. 破坏性 / 需要用户感知与适配（changelog 必列 + 迁移指南必写）

| # | 变更 | 证据（file:line） | 影响与说明 |
|---|---|---|---|
| A1 | **`/api/v1/ai-config/test`、`/api/v1/ai-config/models` 收紧为仅 admin**：普通登录用户调用从 200 变 403（文案「需要管理员权限」）。这是本轮唯一新增的 403 面（其余 admin 端点原本就 403，不变） | internal/api/api.go:158-159（移入 `ar` RequireAdmin 组）、api.go:133（GET /ai-config 留在登录组）；internal/auth/middleware.go:40-43（403 文案）；internal/api/api_security_test.go 锚定 | 动机：这两个端点会解密已保存 Key 并向用户自填地址发起外联（P0 越权外带）。**前端普通用户 UX**：「AI 录入助手配置」卡片**没有 admin 门控**（web/src/views/Settings.vue:36-95 无 `v-if`），仍整卡可见；GET 回显正常（api.go:133 登录即可）；点「获取模型列表」→ ElMessage.error("需要管理员权限")（Settings.vue:223-235）；点「测试连接」→ 红色 alert「连接失败 需要管理员权限」（Settings.vue:246-254 + 84-91）；「保存配置」403 为**既有行为**（api.go:157 原本就在 admin 组）。api.ts 只特殊处理 401（web/src/api.ts），403 走通用错误文案——功能不崩、有提示，但建议下一版给卡片/按钮加 `me.isAdmin` 门控（非阻塞） |
| A2 | **CSV 导入的 notes 变敏感字段 + 默认 AI 不可见**（Bitwarden/Chrome 等所有 CSV 格式共用此映射） | internal/importer/importer.go:114（`add("notes", ..., true, true)` is_secret=true，描述改「备注（常含恢复码等敏感信息）」）、importer.go:117+122（`ai_visible = (notes == "")`） | 导入后 notes 在列表/导出中遮蔽为 `***`，取明文需 reveal（记审计）；含 notes 的条目对 AI 令牌完全不可见，需用户在 Web UI 手动打开 ai_visible。这是导入语义的**预期变化**（r1-security #15 ✅） |
| A3 | **CSV 严格化**：`LazyQuotes=false`，未闭合引号等畸形 CSV 报错拒绝 | internal/importer/importer.go:57-62 | 此前能"导入成功"（静默吞引号/产脏数据）的畸形 CSV 现在整体报错。Bitwarden/Chrome 官方导出为规范 RFC4180，合法文件不受影响（r1-security #16 核验）；keyHive 自身导出走 JSON 不经此路径 |
| A4 | **空 / 纯空白 master.key 拒绝启动** | internal/crypto/crypto.go:140-141（报错并指引「删除该文件后重启以自动生成新密钥」） | 此前空文件派生常量 `SHA-256("")` 也能"正常"运行（加密形同虚设）；现在启动失败。注意迁移提示：删除 master.key 重新生成 = 旧库数据不可解密，指南需写明"仅适用于尚无数据的实例" |
| A5 | **全站请求体 10MB 上限（413）**：含未登录端点与 `/api/v1/ai/*` Bearer token 子树 | internal/api/api.go:24+31-42（LimitBody）、:54-68（readBody 识别 MaxBytesError → 413）、:104（人用路由挂载）；cmd/keyhive/main.go:122（顶层挂载，覆盖 ai 子树）；cmd/keyhive/limitbody_ai_test.go 锚定 | 超限响应 `413 {"error":"请求体超过 10MB 上限"}`；此前超大 JSON 被完整读入内存。CLI/前端正常负载远小于 10MB，实际用户基本无感；自动化脚本若上传超大 body 需适配 |
| A6 | **Anthropic 协议 + 空 baseURL 的出站端点变化**：x-api-key 从错发 `open.bigmodel.cn`（智谱 OpenAI 端点）改为正确发往 `https://api.anthropic.com` | internal/aichat/aichat.go:45-53（defaultBaseURL 按协议分派）+ :224（Chat）、stream.go:31、models.go:19/:108、testconn.go:13；internal/aichat/anthropic.go:23-27+194；internal/api/aichat_api.go:31（currentLLMConfig 不再预填默认端点）；internal/aichat/endpoint_bug_test.go 锚定 | 这是 r1 ❌#12/N1 的收尾修复（生产路径不再被预填架空）。**附带 API 契约变化**：GET /ai-config 在未设置 base_url 时**不再下发该字段**（aichat_api.go:78-79，此前恒回显预填的智谱端点）；自有前端兼容（Settings.vue loadAI 展开缺失字段为空），第三方脚本若假设字段恒存在需适配。副作用修复：前端回显→保存不再把伪造默认端点固化进库 |
| A7 | **CLI flag 打错不再带默认参数执行**：rotate-key / edit / export / import 的 flag 解析失败改为打印用法 + 退出码 2 + **零请求** | internal/cli/admincli.go:94-99（adminFlags 返回 error）、:310-311（rotate-key）、:353-358（edit）、:115（export）、:176（import） | 此前 `keyhive rotate-key --dry-run`（未知 flag）会被忽略并**真实执行主密钥轮换**且退出码 0。脚本用户可见退出码变化（0→2）；这是安全修复，changelog 应显著标注 |
| A8 | **MCP 工具错误结果 IsError=true** | internal/mcpserver/mcp.go:136+139（NewToolResultError 替换纯文本） | MCP 客户端（ZCode/Claude/Cursor）此前把「请求失败/HTTP nnn」当正常工具输出送进模型上下文，现在走标准错误通道。对外契约变化，AI 侧工作流可感知（方向为改进） |
| A9 | **Anthropic 流式 error 事件传播为错误** | internal/aichat/anthropic.go:290-300（error 事件 → 返回错误）；internal/api/aichat_api.go:298（SSE error 事件下发前端） | 此前流中途失败（限流/credit 不足/overload）表现为「空/半截回复 + done 成功」；现在用户看到明确错误。前端聊天与 API 消费方行为变化 |
| A10 | **HTTP server 超时生效**：ReadHeaderTimeout 10s、IdleTimeout 120s（无 WriteTimeout 保 SSE）；且 runServe 改用配置后的 server（此前 `http.ListenAndServe` 另建零配置 server，超时形同虚设） | cmd/keyhive/main.go:69-70、:148-153 | 慢速头部连接 10s 被断、闲置连接 120s 回收。正常浏览器/CLI 无感；长连接心跳类客户端需适配（本项目无此类官方客户端） |

### B. 兼容（新增能力 / 改进，默认行为不变，changelog 推荐）

| # | 变更 | 证据（file:line） |
|---|---|---|
| B1 | **`/export?masked=1` 服务端遮蔽导出**：明文不出服务端进程；审计 detail 记录 masked 标志。明文导出（默认）行为不变 | internal/api/api.go:165-180；前端 web/src/views/EntriesList.vue:299-301（?masked=1 + 客户端脱敏双保险）；CLI internal/cli/admincli.go:124-130（--masked 请求参数，对旧服务端自动回退客户端遮蔽，幂等兼容） |
| B2 | **主密钥轮换重加密范围扩大到 settings 表密文**（含 ai_llm_api_key），同事务原子完成；轮换后 AI 配置不再失效 | internal/store/store.go:363-460（RotateAllEntries 扩展）、:413（`LIKE 'enc:v1:%' AND key != 'key_check'`） |
| B3 | **轮换与条目写并发互斥（TOCTOU 关闭）**：Create/Update/Delete/RotateAllEntries 共用进程级写锁 | internal/store/store.go:29-34（writeMu）、:125-126/:155-156/:195-196（写方法加锁）；store_concurrent_test.go 锚定。r1 建议 A1 已落地最小版（03-refactor-plans.md A1 已标注「R2 已落地」） |
| B4 | **ListEntries 单条坏行容错**：损坏行跳过并记服务端日志，列表/导出不再因单条坏行 500；命中行全部损坏仍整体报错（防密钥不匹配被降级成空列表） | internal/store/store.go:100-104（scanEntry 带出 ID）、:222-246。注意残余：export 会静默跳过坏行（有日志可查，r1-go P2 #4 建议加 meta 标注，未做） |
| B5 | **5xx 内部错误统一通用文案**（完整错误只进服务端日志），覆盖 api 与 aiapi 包 | internal/api/api.go:55-59（writeInternalErr）+ 全部替换点；internal/aiapi/aiapi.go:32-39 + :66/:99（r1 N3 已修）。rotate.go:22/:35 保留 err.Error() 属 admin 诊断信息，r1 认定可接受 |
| B6 | **审计 detail 改 json.Marshal 安全构造**：标题/字段名含引号、反斜杠不再写坏审计 JSON（取证契约修复，18 处调用点） | internal/audit/audit.go:12-17（Detail helper）；api.go/admin.go/users.go/webui.go/aichat_api.go/rotate.go/aiapi.go 各调用点 |
| B7 | **敏感值一律加密**：明文恰好以 `enc:v1:` 开头时不再原样入库成"伪密文" | internal/store/store.go:51-60（去掉 IsEncrypted 跳过） |
| B8 | **畸形 PHC 口令串不再 panic、空密文不再恒通过** | internal/auth/password.go:61-63 |
| B9 | **CLI 配置文件写后 chmod 0600**（Windows 只读位语义差异已忽略） | internal/cli/login.go:118-121 |
| B10 | **CLI 批量导入复用单次登录会话**：N 条导入 = 1 次 argon2id 验证 + 1 条登录审计（服务端开销与审计条数随条数下降） | internal/cli/admincli.go:218-233（cmdImport）、:273-288（importKeyhive） |
| B11 | **前端：设置页改密失败弹错误**（旧密码错等此前点击无反应） | web/src/views/Settings.vue:156-166 |
| B12 | **前端：条目列表搜索/筛选竞态修复**（乱序旧响应不再覆盖新结果） | web/src/views/EntriesList.vue:196-218（loadSeq） |
| B13 | **交付链**：.dockerignore（data/ 含 master.key 不再进构建上下文）；镜像 /app/data 预建属主（非 root 命名卷首启不再 EACCES）；CI 镜像构建注入 VERSION（ACR 镜像 /version 不再恒 dev）；release workflow 权限降到 job 级；新增 push/PR 测试流水线（vet + go test -race + vitest） | .dockerignore（新）；Dockerfile:30；.github/workflows/docker-image.yml（两 job 各注入 --build-arg VERSION）；.github/workflows/release-binary.yml（权限最小化）；.github/workflows/ci-test.yml（新） |

### C. 纯修复（内部 / 防御性，用户基本无感）

| # | 变更 | 证据 |
|---|---|---|
| C1 | aiapi Reveal 审计 detail 安全构造（随 B6） | internal/aiapi/aiapi.go:135-136 |
| C2 | /api/v1/ai 子树纳入 body 限制（r1 N2 修复，随 A5 的"全站"声明落实） | cmd/keyhive/main.go:120-122 |
| C3 | writeReadErr 统一请求体错误出口（413/400 区分） | internal/api/api.go:61-69 |
| C4 | 请求体读取失败的错误信息区分（此前解码错误与业务 400 混同） | internal/api/api.go:71-79（readBody） |

## README 更新点

逐节核对 README.md（181 行）与新行为，**无新增环境变量**（确认过程：`git diff` 全量 grep，`os.Getenv` 仅既有 KEYHIVE_ADMIN_PASS 三处调用，KEYHIVE_ADDR/KEYHIVE_DATA/KEYHIVE_MASTER_KEY/KEYHIVE_KEYFILE 读取逻辑未变），配置表 L165-170 无需加行。需要修改的点：

| 位置 | 问题 | 建议改法 |
|---|---|---|
| README.md:130（CLI import 行） | 未反映 A2/A3 导入语义变化 | 行下或"备份"节附近补 2-3 句：notes 按敏感字段导入（遮蔽/reveal 审计）、含 notes 条目默认 AI 不可见、畸形 CSV 直接报错（请用官方导出） |
| README.md:163-170（配置表） | 未提 A4：master.key / KEYHIVE_KEYFILE 指向空文件时拒绝启动 | KEYHIVE_KEYFILE 行说明或表下补一句"密钥文件为空/纯空白将拒绝启动（防误创建导致的形同虚设加密）" |
| README.md:32-43（Docker Run / 群晖） | sre r1 ⚠️ 未落地：bind mount 宿主目录属主提示缺失（镜像内 chown 管不到宿主已存在目录） | 补一句：宿主目录需可被 uid 1000 写入（`chown -R 1000 <目录>`），否则 Linux 上首次启动 EACCES |
| README.md:55-63（升级节） | CHANGELOG 生成后无入口 | 补"版本历史见 Releases/CHANGELOG"链接 |
| README.md:74-113（AI 接入） | 可选：未提 10MB 请求体上限（A5） | 低优先，可在"安全建议"补一句 |
| README.md:68（裸机"已预构建 web/dist 可跳过"） | 依赖 dist 与源同步（见阻塞项 1） | 发版时 rebuild 后该句自然成立；无需改文案 |

无需改：AI 接入示例（端点/scope 不变）、CLI 命令列表（export --masked 文案 L132 已存在且语义未变）、MCP 节（IsError 为质量改进）、备份节（遮蔽版不能恢复的提示 L131 已有）。

## 版本与发版建议

**版本号：建议 v1.4.0（minor，非 patch）。** 依据：A 类含权限收紧（新 403 面）、导入语义变化、启动拒绝、413 新增、CLI 退出码变化、API 契约微变（base_url 可缺失）——均为"用户可能需要适配"的行为变化，超出 patch 范畴；且 docs/review/03-refactor-plans.md 的后续排期本身以 v1.4.0 为下一版目标，口径一致。

**发版流程提醒（本仓库既定约定）：**

1. **【硬性】改了前端必须 rebuild 并提交 `web/dist/index.html`**（被 git 跟踪；`web/dist/assets/` 被 .gitignore 排除不入库，入口 hash 变化体现在 index.html）。**当前 dist 是旧构建**：assets mtime 为 10-27 19:38，而本轮前端源码 mtime 为 10-28 11:48（Settings.vue/EntriesList.vue）；实证 `grep "masked=1" web/dist/assets/EntriesList-*.js` 为 0 命中（EntriesList.vue:301 的新代码不在产物中），且 `git status` 显示 index.html 无改动。**提交前必须 `cd web && npm run build`**，否则 README:68"已预构建 dist 可跳过"的裸机构建路径会嵌入旧前端（CI 二进制与 Docker 镜像在流水线内自行构建，不受影响，但仓库一致性要求提交）。
2. **二进制发版**：推 `v*` tag 自动触发 .github/workflows/release-binary.yml（或手动 dispatch 输入版本号）→ 全平台产物 + sha256 挂 Release。注意发版流水线**不跑测试**（测试门禁仅 ci-test.yml 的 push(main)/PR，r1-sre G1 的 needs 关联未做，属已知残余）。
3. **镜像流水线**：手动 workflow_dispatch（target_registry / image_tags / arch）；本轮起自动从 image_tags 解析 VERSION 注入（docker-image.yml 两个 arch job），ACR 镜像 /version 将显示真实版本号。
4. **CHANGELOG.md 尚不存在**（仓库根无该文件），按任务约定下一步生成——本文 A/B 两表即其素材；迁移指南重点覆盖：A1（普通用户 Settings 页按钮 403）、A2（导入的 notes 遮蔽与 AI 不可见）、A4（空 master.key 处置）、A5（413）、A7（脚本退出码）。

## 阻塞项与残余风险

**阻塞项（提交/打 tag 前必须处理）：**

| # | 事项 | 状态 |
|---|---|---|
| 1 | web/dist 未 rebuild（见发版流程第 1 条） | **未完成，唯一硬阻塞**。rebuild 后 `git status` 应出现 `M web/dist/index.html` 并随提交 |
| 2 | CHANGELOG.md / 迁移指南 / PR 描述未写 | 待生成（下一步，本文为素材） |

**r1 四份复审 ⚠️/❌ 项闭环核对（本轮已修复的先说结论：全部关键项已闭环）：**

| r1 项 | 状态 | 证据 |
|---|---|---|
| r1-security ❌ #12 / N1（anthropic 端点被 api 层预填架空） | **已修** | aichat_api.go:31 不预填 + :78-79 回显不伪造 + internal/aichat/endpoint_bug_test.go 锚定 |
| r1-security ⚠️ #2 / N2（/api/v1/ai 无 body 限制） | **已修** | main.go:122 顶层 `r.Use(api.LimitBody)` + cmd/keyhive/limitbody_ai_test.go |
| r1-security ⚠️ #5 / N3（aiapi 500 回显） | **已修** | aiapi.go:32-39 writeInternalErr（ListEntries/Search/Reveal 三处替换） |
| r1-security 建议 A1（轮换写互斥） | **已落地最小版** | store.go:29-34 writeMu；03-refactor-plans.md A1 已标注「R2 已落地」 |
| r1-sre P1（CI 测试门禁） | **部分落地** | ci-test.yml 已建（push main/PR：vet + race + vitest）；但 release/docker 流水线未设 needs 测试门禁，发版仍可在红测状态产出（残余） |
| r1-sre ⚠️ .dockerignore 缺 `.env*` 与根级 `keyhive-*.tar.gz/.zip` | 未补（.dockerignore 现内容无此两项） | P3 防御性，不阻塞 |
| r1-sre ⚠️ bind mount 属主 README 说明 | 未补 | 本报告 README 更新点第 3 行 |
| r1-sre ⚠️ P2 慢速 body 无 ReadTimeout（现仅 ReadHeaderTimeout） | 残余，已归 03-refactor-plans | 不设 ReadTimeout 是为 SSE 保留，方向正确 |
| r1-frontend ⚠️ /categories 二次 await 后未复查 seq | 残余观察项（写入幂等，低风险） | EntriesList.vue:205 |
| r1-frontend P2（Audit.vue 翻页竞态未修且未入重构路线） | 残余，需补进 03-refactor-plans F1 | r1-frontend 建议已记录 |
| r1-go P2 #4（导出坏行静默跳过无 meta 标注） | 残余 | store.go:222-246 现状 |
| r1-security N4/N5/N6/N7/N8（上游回显 200 字符、400 透 SQLite、GET /ai-config 登录开放、enc:v1: 前缀 settings 轮换中止、`***` 碰撞） | 残余提示级，均有归属（A4/C1 等） | 03-refactor-plans.md 专项 A/C |

**本轮新识别残余（非阻塞）：** 普通用户 Settings 页 AI 配置卡无 admin 门控（A1 详述），建议下一版加 `v-if="me.isAdmin"` 或按钮禁用态。

## 总体结论

**有条件就绪（conditional GO，建议 v1.4.0）。** 代码侧 25+ 项修复经三层核验（02-test-report 自报 → r1 四路角色复审 → 本轮 diff 逐条再印证）实现与声明一致，r1 的全部 ❌/关键 ⚠️（N1/N2/N3/A1）均已在工作区闭环且带测试锚点；行为变化方向正确、边界清晰，无可回退性风险（轮换扩展在事务内、遮蔽导出不动明文默认路径）。发版前唯一硬性动作是 **rebuild 前端并提交 web/dist/index.html**；随后按本报告更新 README 三处、生成 CHANGELOG 与迁移指南（素材即上文 A/B 表），再打 `v1.4.0` tag 触发二进制发版并手动 dispatch 镜像流水线。残余项全部低危且有路线归属，不构成本版阻塞。
