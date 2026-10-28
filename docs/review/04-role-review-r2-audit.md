# 第二轮角色复审 · 独立审计（2026-10-28）

> 审计人视角：独立审计员（不信自报，逐项实证）。对象：第一轮角色复审（04-role-review-r1-security.md / r1-go.md）后的 4 项收尾修复（02-test-report.md §3.1 #22-25）。
> 方法：全量 diff 走读 + 调用点 grep + **红态验证**（将仓库复制到临时目录、逐项回退修复后跑测确认必红/概率红，副本已删除，源仓库零改动）+ 独立跑测四件套。
> 结论速览：**4 项修复 = 4 ✅（其中 #24 附带"无测试锚点"缺口）**；独立跑测全绿；第一轮 N1/N2/N3 全部关闭；无回归。总体评级 **A-（可信）**。

---

## 四项修复核验（✅/⚠️/❌ + file:line 依据）

### #22 协议盲预填移除（N1 收尾）—— ✅

- **实现**：`internal/api/aichat_api.go:31` `BaseURL: strings.TrimSpace(s.getSetting(settingLLMBaseURL))`——不再 `orDefault(..., aichat.DefaultBaseURL)`，空值原样透传给 aichat 层；`GET /ai-config`（aichat_api.go:78-80）只在已存且非空时才下发 `base_url` 字段（存什么回什么，未存即缺席，杜绝前端回显→保存的持久化固化链）；test/models 的回退链 `orDefault(req.BaseURL, saved.BaseURL)`（aichat_api.go:376、407）两端皆空时 BaseURL 为空串，由 aichat 层分派——第一轮 N1 的三个点全部收口。
- **aichat 层分派（修复生效前提）**：`aichat.go:48-53 defaultBaseURL()` 按协议分派；五处入口 `BaseURL == ""` 时填充（aichat.go:223-224、stream.go:30-31、testconn.go:12-13、models.go:18-19/107-109）。
- **全仓 grep 无其他协议盲预填**：`DefaultBaseURL` 生产引用仅剩 aichat 包内 defaultBaseURL 分派与常量定义本身；api 层唯一残余是注释。
- **测试真实锚定**：`internal/api/aichat_api_protocol_test.go` 4 个用例——单元（TestCurrentLLMConfigNoBaseURLPrefill：未存即空 + 显式值透传）、端到端（TestAnthropicEmptyBaseURLGoesToAnthropicEndpoint：经 PUT/GET/test/chat 全链路，用 HTTPS_PROXY 拒绝代理捕获外发 CONNECT host，断言必须命中 `api.anthropic.com`）、openai 对照（仍指 bigmodel，行为不回退）、显式 URL 对照（不改写、路径 `/v1/messages`）。
- **红态验证（我做了）**：临时副本把 :31 改回 `orDefault(..., aichat.DefaultBaseURL)` → 2 测必红，失败信息直接实锤 `外发请求被发往 "open.bigmodel.cn"，应为 "api.anthropic.com"`。**"先红态验证"声明可信**：测试观测的是真实外发目标而非实现细节，代理拒绝设计（ProxyFromEnvironment + loopback 直连豁免不影响包内其他 mock 测试，全量测试绿已证）自洽。

### #23 body 限制覆盖 AI 子树（N2 收尾）—— ✅

- **实现**：`internal/api/api.go:31-42` `LimitBody` 导出（ContentLength 预判 413 + MaxBytesReader 包裹）；`cmd/keyhive/main.go:122` 顶层 `r.Use(api.LimitBody)`——先于 `/api/v1` 与 `/api/v1/ai` 两个子树挂载，AI 子树（含 Reveal）纳入限制；人用 Routes 内的挂载保留（api.go:104），双重包裹同限值行为等价（api.go:29-30 注释说明）。
- **静态资源不受影响**：GET 请求 `ContentLength=0` 不触发预判，body 为 `http.NoBody` 不被包裹；`cmd/keyhive/spa_test.go`（静态缓存头）与全量测试绿佐证。
- **测试真实锚定**：`cmd/keyhive/limitbody_ai_test.go` 11MB JSON 打 `/api/v1/ai/entries/1/reveal` 断言 413（在鉴权/解码之前），并带对照组：同路径小 body 无 token → 401，证明 413 由体积触发而非路径封禁。
- **红态验证（我做了）**：副本删除 main.go:122 挂载行 → TestAISubtreeBodyLimit 必红（`应 413，实际 401`），精确复现第一轮 N2 症状。

### #24 aiapi 5xx 不回显原文（N3 收尾）—— ✅（附：无测试锚点）

- **实现**：`internal/aiapi/aiapi.go:35-38` 新增 `writeInternalErr`（日志 `[aiapi] internal error` + 通用文案"服务器内部错误"），两处调用点 `:66`（ListEntries）、`:99`（Search）替换原 `writeErr(w, 500, err.Error())`。
- **grep 确认无残留**：aiapi 包（含全部测试文件）`err.Error()` 零匹配；其余非 5xx 回显均为固定文案或用户自身输入（如 aiapi.go:129 字段名回显，无内部信息）。
- **⚠️ 无测试锚点**：`aiapi_edge_test.go:147-174 TestClosedDBHandlers` 只断言状态码 500、不断言文案——红态验证（副本回退为 err.Error() 直传）后 aiapi + cmd/keyhive 全部测试**仍绿**。02-test-report 的"先红态验证"声明仅覆盖 3 个新测试文件、未声称 #24 有锚点，故不算虚报，但该修复当前无回归防线（见残余风险 R1）。

### #25 store writeMu 最小写互斥（A1 提前）—— ✅

- **实现**：`internal/store/store.go:34` `writeMu sync.Mutex`；四处入口加锁——CreateEntry（:125-126）、UpdateEntry（:155-156）、DeleteEntry（:195-196）、RotateAllEntries（:364-365）。
- **无重入死锁路径**：RotateAllEntries 内部只走直接 SQL（`s.DB.Query`/`tx.Exec`）与 `encryptFieldsWith`/`decryptFields`，不调用任何已锁写方法；其调用的 `s.cipher()`（:38-42）取的是另一把锁 `mu`（RWMutex，:27）；UpdateEntry 内部调用的 GetEntry（:189-191）是读方法不加锁——全图无同一 Mutex 的嵌套获取。
- **测试确定性评估**：`store_concurrent_test.go` 断言的是**终态**（并发结束后：条数 `initial+workers*perWorker` 一条不少 + 逐条解密回登记明文 + 二次轮换后复验），不依赖 goroutine 调度时序；ListEntries 静默跳坏行的缺口由"条数校验"兜住——断言设计正确。
- **红态验证（我做了，量化）**：副本删除全部 writeMu.Lock/Unlock 后独立跑 10 次 → **10/10 红**（典型失败：`条数应 100，实际 86`，14 个条目以旧钥密文残留、永久不可解密——真实数据丢失而非报错）；源仓库（有锁）同测 10 次 → **10/10 绿**。注意：无锁红态本质是概率性交错命中（本机负载下 100% 命中，理论上存在偶绿可能），测试注释的"与调度时序无关"指断言方式，红态命中率则靠初始 40 条拉长轮换窗口 + 4 worker×15 发令枪并发来保证——设计合理。
- **批量删除/复制是否漏锁（按要求列出，不算失败）**：核对结论是**无漏锁**——后端不存在批量删除/复制的独立 store 方法；前端批量删除逐条 `DELETE /entries/{id}`（EntriesList.vue:251-277）、复制经新建页落 `POST /entries`（EntriesList.vue:236-238）、Web 导入循环 CreateEntry（webui.go:111/146），全部收敛到已锁方法。模板写方法（SaveTemplate/DeleteTemplate）未上锁但**无需**：模板字段值一律清空（store.go:327）、不产生密文、不参与轮换（轮换 LIKE 'enc:v1:%' 不会命中模板行）。**唯一例外**：`putSetting` 密文写路径未过 writeMu（见残余风险 R2）。

## 独立跑测结果

全部在源仓库独立执行（非复述自报）：

| 命令 | 结果 | 退出码 |
|---|---|---:|
| `go build ./...` | 通过 | **0** |
| `go vet ./...` | 通过，零告警 | **0** |
| `go test ./... -count=1` | 17 包全 ok（web 无测试文件） | **0** |
| `cd web && npx vitest run` | 13 文件 / **141/141 通过** | **0** |

## 一致性抽查

- **02-test-report §3.1 行号 vs 实际 diff**：#22 `aichat_api.go:31`（该行正是 BaseURL 赋值行）✓；#23 `api.go:31`（LimitBody 函数定义行）、`main.go:122`（r.Use 挂载行）✓；#24 `aiapi.go:35`（writeInternalErr 定义行）✓；#25 `store.go:34`（writeMu 字段行）✓。**四处行号全部精确对应**。
- **03-refactor-plans A1**：已更新为"R2 已落地最小版：store.writeMu 覆盖 Create/Update/Delete/RotateAllEntries（并发测试锚定）"——与代码现状一致 ✓；"剩余"项中"批量删除/复制的锁覆盖核对"本次已完成（结论：均收敛到已锁方法，无需补锁），但未点名 putSetting（见 R2，建议补录）。
- **03-refactor-plans G1**：声明 ci-test.yml 已落地（vet + `go test ./... -race` + vitest、最小权限）——`.github/workflows/ci-test.yml` 实际内容一致（push main + PR 触发，`permissions: contents: read`，backend/frontend 两 job）✓。

## 残余风险

1. **R1【低】#24 无测试锚点**：aiapi 5xx 文案收敛后，回退无测试可拦截。建议 TestClosedDBHandlers 顺手加一行断言：500 响应体含"服务器内部错误"且不含 `sql`/`database` 字样（S 级工作量）。
2. **R2【低】putSetting 密文写未纳入 writeMu**：`aichat_api.go:42-46` 直接 SQL 写 settings；轮换预读（store.go:413）之后、换钥（:475-477）之前若有并发 `PUT /ai-config` 保存 LLM API Key（旧钥加密落库、不在 settingUpdates 内、事务不覆盖它），换钥后该 Key 永久不可解密——表现为 AI 功能报"API Key 解密失败"，管理员重存即恢复，不损条目数据。触发面窄（轮换秒级窗口内恰逢保存 AI Key），属 03-refactor-plans A1"剩余"应补录的项。
3. **R3【提示】aiapi Reveal 裸解码的状态码语义**（aiapi.go:118）：body 已被 MaxBytesReader 保护（内存安全），但超限时 Decode 报 MaxBytesError → 落 400 而非 413（api 包同类问题第一轮 P3 已记录，N2 修复后内存面已闭环，仅语义打磨）。
4. **R4【既定遗留，本轮无恶化】**：400 路径 err.Error() 回显（N5）、上游错误体 200 字符透传（N4/A4）、store_concurrent_test 红态概率性本质（CI `-race` 提供 CI 侧第二道防线，本机 Windows 无 cgo 未跑）。

## 终审结论

**4 项收尾修复全部真实落地、无回归，评级 A-（可信）。**

- #22/#23/#25 三项均带测试锚点且经本审计独立红态回退验证必红（#25 无锁 10/10 红量化确认），锚点断言的是正确行为（外发目标 host、终态条数+逐条 round-trip），无弱化痕迹；#24 实现正确、grep 零残留，唯缺锚点（R1）。
- 第一轮复审阻塞项全部关闭：N1（anthropic Key 错发智谱端点）生产链路已断根，N2（AI 子树 body 裸奔）已覆盖，N3（aiapi 5xx 回显）已收敛；A1 最小互斥提前落地且经并发量化验证。
- 独立跑测四件套全绿（退出码均 0），与 02-test-report §4 自报一致；§3.1 行号与实际代码逐一吻合，未发现虚报或选择性陈述。
- 合入建议：可直接合入；R1/R2 作为小尾巴随下一批（R1 一行断言，R2 归入 A1 剩余）。
