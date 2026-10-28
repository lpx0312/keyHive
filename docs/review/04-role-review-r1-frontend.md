# 前端第一轮角色复审（2026-10-28）

> 复审人角色：资深前端（Vue3 / Element Plus / Vitest）。范围：本轮 `git diff -- web/` 全部改动
> （Settings.vue 改密错误处理、EntriesList.vue 搜索请求序号 loadSeq、遮蔽导出 `?masked=1` 双保险、
> ChatDrawer.test.ts 新增、两条 test.fails 翻正、api/ui 测试扩充）+ 独立跑测 + 文档一致性 + 遗留优先级。
> 方法：逐行读改动与被测组件源码、读后端契约实现交叉验证、CLI 模拟覆盖率口径（不改任何文件）、
> 本机独立执行 `npx vitest run` 与 `--coverage`。

## 实现核验（✅/⚠️/❌+file:line）

### 1. Settings 改密错误处理 — ✅

- `web/src/views/Settings.vue:153-167`：`change()` 的 `await api('/password', …)` 已整体包进 try/catch，
  失败走 `ElMessage.error(e.message)`，成功路径（提示 + 1s 后跳 /login）不变。
- 两条失败路径均真实可达且被覆盖：
  - **网络层 reject**：`api()`（`web/src/api.ts:2-17`）内部无兜底 catch，fetch 的 reject 原样透传
    ——本轮新增 `web/src/api.test.ts:80-83`（fetch reject 传播 TypeError）锚定了该契约，Settings 的
    catch 因此必然能接住网络失败。
  - **4xx 业务错误**：`api.ts:13-15` 非 2xx 抛 `Error(data.error)`（如"旧密码不正确"）——
    `Settings.test.ts:323-339` 用真 ElMessage 记录层断言 error toast 文案，行为级验证通过。
- 次要观察（不扣分）：401 时 `api()` 先跳 /login 再抛"未登录"，catch 会弹一条即将被页面跳转吞掉的
  提示，纯视觉噪音，可忽略。

### 2. EntriesList loadSeq 竞态防护 — ✅（附 1 条 ⚠️ 观察项）

守卫本体 `web/src/views/EntriesList.vue:192-216`：入口 `const seq = ++loadSeq`，`/entries` 响应后
`seq !== loadSeq` 即作废（:203）、报错弹窗按最新序号门控（:213）、loading 复位门控（:215）——
check 与 `entries.value = list` 赋值之间无 await，原子成立。

**全部异步入口逐一核对**（漏一处就还有竞态）：

| 入口 | 位置 | 是否经 load() → 受 loadSeq 保护 |
|---|---|---|
| 搜索输入 `@input` | EntriesList.vue:9 | ✅ |
| 分类切换 `@change` | EntriesList.vue:10 | ✅ |
| 删除单条后刷新 | EntriesList.vue:244 | ✅ |
| 批量删除后刷新 | EntriesList.vue:275 | ✅ |
| 导入成功后刷新 | EntriesList.vue:364 | ✅ |
| AI 入库广播 `ENTRIES_CHANGED_EVENT` | EntriesList.vue:373 | ✅ |
| 挂载初始加载 | EntriesList.vue:377 | ✅ |
| 分页 / 改每页条数 | EntriesList.vue:100-102,169-173 | ✅ 本地 computed 切片，无网络请求，天然无竞态 |
| `loadStaleDays()` | EntriesList.vue:220-222 | 不写 entries，与列表无共享状态，无竞态 |

结论：**无遗漏入口**。回归测试 `EntriesList.test.ts:373-400` 用受控 deferred promise 构造
"先发慢（q=a）+ 后发快（q=ab）"时序，断言旧响应不覆盖新结果——真正打到守卫分支，而非只测 mock。

- ⚠️ **观察项**：`/categories` 这个第二次 await（EntriesList.vue:205）之后未再复查 seq 就写
  `categories.value` 和页码钳制（:206-211）。当前两处写入均幂等或与查询参数无关（categories 不随
  q/category 变化；页码钳制是当前状态的确定性函数），无用户可见实害；但若未来在该 await 之后追加
  依赖查询参数的逻辑，需补 `if (seq !== loadSeq) return` 复查。建议留注释锚定即可，不必本轮改。

### 3. 遮蔽导出双保险 — ✅（幂等成立）

- 客户端：`EntriesList.vue:301` masked 模式请求 `GET /export?masked=1`；`EntriesList.vue:302-304`
  对 `is_secret` 字段循环置 `'***'`。
- 服务端：`internal/api/api.go:162-179`，`?masked=1`（或 `true`）时逐条 `MaskSecrets()`；
  `internal/model/model.go:81-87` 把 `IsSecret` 字段 Value 置为 `MaskedValue`。
- **幂等性**：两侧谓词相同（is_secret）、占位常量相同（model.go:23 `MaskedValue = "***"`，与前端
  字面量一致）。服务端已遮蔽时客户端再写一遍 `'***'` 结果不变；服务端契约未生效（返回明文）时客户端
  兜底脱敏——"双保险"语义准确。导出路由挂 admin 组、UI 侧按钮也按 `me.isAdmin` 显隐
  （EntriesList.vue:18），无越权面。
- 测试侧 mock 已同步改前缀匹配兼容 `?masked=1`（EntriesList.test.ts:27,258）。

### 4. ChatDrawer 测试质量 — ✅（测的是组件行为，不是 mock）

`web/src/components/ChatDrawer.test.ts`（8 用例）的真实成分：

- 挂载**真实 ChatDrawer 组件 + 真实 ElementPlus**，仅 mock 三个纯依赖模块：`../api`（只拦草稿入库
  的 POST /entries）、`../store`（notifyEntriesChanged + 事件名常量）、`../ui`（copyText）。
- **SSE 主链路是真实执行**：fetch mock 返回真 `ReadableStream`，组件内 `getReader()/TextDecoder/
  split('\n\n')/JSON.parse/handleEvent` 整段解析循环（ChatDrawer.vue:239-256）被真实跑通，断言落在
  渲染 DOM（用户消息上屏、status 行、草稿卡、按钮 disabled 复位）、localStorage、请求体内容上。
- 敏感值遮蔽有硬断言：`super-secret-123` 绝不出现在 DOM（test.ts:168）。
- 事件常量 mock 值 `'keyhive:fab-hidden-changed'` 与真实 `store.ts` 常量一致（非虚构契约），
  contextmenu 隐藏 → 写 localStorage → 设置页恢复的跨 chunk 链路两端都有测。
- 诚实披露：文件头注明 coverage.exclude 仍排除 ChatDrawer.vue（见下文 F2）。

### 5. 两条 test.fails 翻正 — ✅

`grep -rn "test.fails|it.fails" web/src/` 无残留；对应两条新常规绿测
（EntriesList.test.ts:373、Settings.test.ts:323）本轮独立跑全部通过。

## 独立跑测结果

本机（Windows + Git Bash，`cd web`）独立执行，与改动方无关：

| 命令 | 结果 |
|---|---|
| `npx vitest run` | **13 个测试文件 / 141 个用例全部通过**（6.52s） |
| `npx vitest run --coverage` | 全绿；All files：Stmts **93.46%** / Branch **90.87%** / Funcs **90.76%** / Lines **96.76%**，四维 thresholds（均 90）通过，无 ERROR |

- 与 02-test-report.md、web-frontend.md 声称的"141 测全绿、四维 ≥90"完全一致，**可复现**。
- 过程噪音：EntriesList 竞态用例 stderr 有 `ElOnlyChild no valid child node` 警告（el-tooltip 空插槽
  渲染警告，不影响断言，属既有模式），不构成问题。
- 追加取证（评估 F2 用，**未改任何文件**）：用 CLI `--coverage.include/--coverage.exclude` 模拟把
  ChatDrawer.vue 纳入口径 → ChatDrawer.vue 单文件 Stmts 75% / **Branch 58.86%** / Funcs 70.21% /
  Lines 84.76%，全局 Branch 跌到 **85.33%**、Funcs **88.84%**，两条 thresholds ERROR。

## 文档一致性（docs/modules/web-frontend.md 抽查）

| 文档断言 | 核验 |
|---|---|
| 遮蔽导出 `GET /export?masked=1` + 客户端循环置 `***` 双保险（:48、:59） | ✅ 与 EntriesList.vue:301-304、api.go:162-179 一致 |
| 请求序号模式 4 步描述（:84-88） | ✅ 与 load() 实现逐条吻合（seq 自增/过期 return/报错与 loading 门控/效果） |
| Settings"改密失败弹错误"（:65） | ✅ 与 Settings.vue:163-166 一致 |
| coverage exclude 含 ChatDrawer.vue + thresholds 四维 90 + "行为回归由 ChatDrawer.test.ts 保证"（:93） | ✅ 与 vitest.config.ts:15-17、新增测试文件一致 |
| 13 个 .test.ts / 141 用例（:94） | ✅ 独立跑测复现；:95 每文件分布逐一核对全对（Login 3 / EntryEdit 22 / EntriesList 22 / Settings 21 / Templates 11 / Tokens 9 / Users 9 / Audit 7 / ChatDrawer 8 / 逻辑层 29） |
| 两个 test.fails 锚点已转常规绿测（:99） | ✅ 无残留 |
| 技术栈版本：Vue 3.5 / EP 2.9 / Vite 6 / Vitest 5 / TS 5.7 / markstream-vue 2.0.14（:8-9） | ✅ 与 package.json 一致；npm scripts 五条也对 |
| ⚠️ :95 "views **七页**各一个（Login 3、EntryEdit 22、…、Audit 7）" | 列了 **8** 个页面测试文件；:26 又正确写"八个页面"——笔误，"七页"应为"八页" |

## 建议（分级）

- **P2（应采纳）**：`docs/review/03-refactor-plans.md` 专项 F 漏收 **Audit.vue 同类竞态**——
  01-review-frontend.md P3-6 明确指出 `Audit.vue:75-99` 的 `load()` 是 `try/finally` 无 catch
  （请求失败无提示、unhandled rejection）且无请求序号（服务端分页 offset，快速翻页旧响应覆盖新页，
  比 EntriesList 的本地分页竞态更真实），本轮已复检确认仍未修，且 03-refactor-plans 全文 grep
  "Audit" 零命中——该发现从路线图中掉了。建议并入 F1（同一套 loadSeq 模式复制即可，S 工作量）。
  F1 现状里"本轮已修请求序号竞态"的表述也易读成"这一类都修了"，宜改为"EntriesList 搜索竞态已修，
  Audit 翻页竞态待修"。
- **P3（建议采纳）**：F2（ChatDrawer 覆盖率口径）工作量标 **S 偏乐观**——实测纳入后 Branch 只有
  58.86%，要过 90 需补拖拽移动、长按隐藏、401/非 ok 响应、保存失败、全屏切换等分支，建议改标 M，
  或拆两步：先移出 exclude + 该文件单独豁免 per-file threshold，再逐步补。优先级 P3 本身合理
  （行为已有 8 条回归测锁住，纯指标卫生）。
- **P3（顺手）**：web-frontend.md:95 "七页"笔误改"八页"；`/categories` 二次 await 后建议补一行
  "此处写入幂等，若追加查询参数相关逻辑需复查 seq"的注释（见实现核验第 2 条观察项）。
- **P3（记录备查）**：01-review-frontend P3-1（ElMessageBox 取消路径 unhandled rejection，
  EntriesList/Templates/Tokens/Users/Settings/EntryEdit 共 7 处）同样未进专项 F；影响仅控制台噪音，
  可留在速赢清单或 F1 一并处理。
- F1 本体（路由守卫 / fetch 超时 / 搜索防抖，P2·M，排 v1.5）：优先级判断合理。路由守卫（P2-1，
  受限页先渲染后 401 踢出）若产品要对外部署可考虑提前到 v1.4.x，纯内网自用维持 v1.5 无异议。

## 总体结论

**通过（无需返工）。** 本轮四项前端改动实现质量高：改密错误处理两条失败路径（网络 reject / 4xx 业务
错误）均真实接住且有行为级测试；loadSeq 覆盖全部 9 个异步入口、无遗漏（分页为本地切片天然免疫），
回归测试真打守卫分支；遮蔽导出双保险幂等成立（两侧谓词与占位常量一致）；ChatDrawer 8 条测试挂真实
组件 + 真实 SSE 流消费，测的是组件行为而非 mock。独立跑测 141/141 全绿、四维覆盖率 93.46/90.87/
90.76/96.76 全过线，与声称一致。文档 web-frontend.md 与代码高度同步（仅"七页"一处笔误）。遗留计划
优先级总体合理，但有一处 P2 级建议：Audit.vue 同类竞态 + 无 catch 从重构路线图中掉落，应补回 F1；
F2 工作量按实测证据建议 S → M。
