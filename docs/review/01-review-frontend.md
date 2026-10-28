# 前端（web）代码审查报告

> 审查人：资深前端代码审查员（安全 + 正确性 + 可维护性，Vue3 专家视角）
> 审查日期：2026-10-28 · 只读审查，未修改任何业务文件

## 概览

- **范围**：`web/src/` 全部 24 个文件（App.vue、main.ts、api.ts、store.ts、ui.ts、base.css、env.d.ts、8 个页面视图及其 8 个 .test.ts、ChatDrawer.vue、test/fixtures.ts），以及 `web/web.go`、`web/index.html`、`web/vite.config.ts`、`web/vitest.config.ts`、`web/tsconfig.json`，合计约 4600 行，逐文件通读未抽样。为核实前端事实交叉验证了后端 `internal/api/api.go`、`internal/store/store.go`、`internal/model/model.go`、`cmd/keyhive/main.go`。
- **总体评价**：这是一套质量明显高于社区平均的自托管前端。整个 `src/` 没有一处 `v-html`、`innerHTML` 或 `dangerouslyUseHTMLString`；列表 URL 提取强制 https 前缀、markstream-vue 默认 safe HTML 策略、会话走 HttpOnly+SameSite=Lax Cookie 且零敏感值落 localStorage，安全基本面扎实。测试对边界（null 响应、遮蔽值、取消分支）覆盖认真。
- 主要短板集中在三处：**遮蔽导出在前端做脱敏（明文仍全量过境）**、**没有任何路由守卫与 fetch 超时/流取消机制**、**若干操作路径缺错误处理（修改密码失败零反馈最典型）**。均可用小改动修复，不涉及架构调整。
- 分级统计：**P0 × 0 · P1 × 1 · P2 × 4 · P3 × 11**。

## 发现清单

### P1 严重

**[P1] 「遮蔽导出」实为前端脱敏：全库明文仍完整到达浏览器**（`web/src/views/EntriesList.vue:290-310`，交叉证据 `internal/api/api.go:115-125` + `internal/store/store.go:81-96`）

- 问题：`doExport()` 无论哪种模式都调用 `await api('/export')`，后端 `exportEntries` → `ListEntries` → `scanEntry` → `decryptFields` 返回的是**解密后的全量明文**；"遮蔽版"只是在 JS 里循环把 `f.value = '***'` 后再生成 Blob。
- 影响：用户选择"遮蔽版（敏感值为 ***，可分享结构）"时，预期是明文不出服务端，实际全部密码明文到达浏览器 —— 停留在 JS 内存、DevTools Network 响应体、以及任何可读网络面板的浏览器扩展可见范围内。对密钥管理器而言，该功能的核心安全承诺在传输层不成立。（缓解因素：`/export` 为 admin-only 且记 `export` 审计，但遮蔽/明文两种意图在服务端行为完全相同。）
- 修复建议：后端 `/export` 支持 `?masked=1`，在 `decryptFields` 后对 `is_secret` 字段写 `***` 再返回；前端遮蔽分支改带参数请求，删除客户端脱敏循环。

### P2 严重程度次之

**[P2] 全站无路由守卫：受限页可直接输 URL 渲染后才被踢**（`web/src/main.ts:9-23`）

- 问题：路由表没有任何 `beforeEach`/`beforeEnter`；`me.loaded` 挂载后也不用于导航控制。直接打开 `/users`、`/audit`、`/tokens`、`/entries` 时页面骨架先完整渲染，首个 API 返回 401 后 `api()` 才 `location.href = '/login'` 整页跳转；非管理员登录后手输 `/audit` 则只收到一条 403 错误提示停留在空页。
- 影响：数据安全由后端 `RequireSession/RequireAdmin` 中间件兜底（`internal/api/api.go:90-114`），**不存在越权读取**；但存在未认证内容闪现、非管理员无引导滞留，以及每次 401 都走整页跳转（丢失 SPA 状态）的问题。
- 修复建议：`main.ts` 加全局 `router.beforeEach`：`me.loaded` 未就绪先 `await loadMe()`，未登录重定向 `/login`；管理路由（users/tokens/audit）校验 `me.isAdmin`，否则重定向 `/entries`。

**[P2] 修改密码失败零反馈：`change()` 无 try/catch**（`web/src/views/Settings.vue:153-162`）

- 问题：`change()` 直接 `await api('/password', ...)` 后就弹成功提示，无 try/catch、无 loading 状态。旧密码错误（最常见失败路径）时 promise 以 unhandled rejection 结束，用户看到的是**点击后毫无反应**。
- 影响：正确性 + 可用性缺陷；同页其余操作（saveAI/saveStale/doRotate）都有 catch，唯独这条最高敏路径没有。
- 修复建议：补 try/catch + ElMessage.error，顺带加 saving 状态防重复提交。

**[P2] fetch 全链路无超时；SSE 流无 AbortController、无取消入口**（`web/src/api.ts:2-17`、`web/src/components/ChatDrawer.vue:227-263`）

- 问题：`api()` 未挂 `AbortSignal.timeout`，网络半开时请求永久 pending。ChatDrawer 的 SSE 消费用原生 `fetch` + `reader.read()` 循环，同样无超时、无 AbortController；发送中「清空对话」按钮 `:disabled="loading"`、输入区 `:disabled="loading"`。
- 影响：LLM 服务商或反代挂起连接时，`loading` 永久为 true，整个输入区锁死，只能刷新页面；用户也没有任何主动终止一次生成的手段（长回复场景常见需求）。
- 修复建议：`api()` 加 `AbortSignal.timeout(15000)`（可按需放宽）；ChatDrawer 持有 `AbortController`，发送按钮旁提供「停止」按钮，组件 `onUnmounted` 时 `abort()`（当前 App 级组件卸载仅发生在整页跳转，但补上是无成本的正确性保证）。

**[P2] 条目搜索无防抖且无请求时序保护：每键两连发 + 乱序覆盖竞态**（`web/src/views/EntriesList.vue:9,192-211`）

- 问题：搜索框 `@input="() => load(true)"` 每个字符触发一次完整 `load()`（`/entries?` + `/categories` 两个请求，后端是对 fields JSON 文本的 5 列 LIKE）；`load()` 内无 requestId/AbortController，响应直接整体赋给 `entries.value`。
- 影响：(a) 大库下每键两次请求，服务端 LIKE 压力放大；(b) 竞态 —— 输入 "abc" 时若 "a" 的响应比 "ab"/"abc" 的后到达（服务端 LIKE 耗时与结果集大小相关，完全可能反序），列表最终显示的是 "a" 的结果，与搜索框内容不一致。
- 修复建议：300ms 防抖 + 每次调用自增 requestId，赋值前校验仍是最新一次；或对前一次请求 `abort()`。Audit.vue 快速翻页同理（见 P3-10）。

### P3 建议与一般问题

**[P3] ElMessageBox 取消路径全部成为 unhandled rejection**（EntriesList.vue:235、248；Templates.vue:204；Tokens.vue:104；Users.vue:98,120；Settings.vue:301；EntryEdit.vue:237）

- Element Plus 的 `confirm/prompt` 在用户点取消时 reject（`'cancel'`）。上述 8 处调用均未 catch，每次用户点「取消」都产生一条 uncaught rejection（Vue 以错误级别记录）。统一加 `.catch(() => {})` 或封装 `confirmSafe()`。

**[P3] copyText 降级链路失败时泄漏含明文的隐藏 textarea**（`web/src/ui.ts:24-33`）

- `execCommand('copy')` 抛异常时直接跳到外层 catch `return false`，`ta.remove()` 不执行 —— 一个 `value` 为敏感明文的 `<textarea>`（opacity:0、position:fixed）残留 DOM。罕见路径但属密钥管理器不该有的残留；把 `ta.remove()` 挪进 `finally` 即可。

**[P3] TOTP 弹窗打开瞬间重复请求与负数进度条**（`web/src/views/EntryEdit.vue:264-272`）

- `showTOTP()` 里首个 `fetchTOTP()` 尚未返回时 interval 已开始每秒 `totpRemaining--`（初值 0 → 首秒即 -1 → `<= 0` 再次触发 `fetchTOTP()`）。慢网络下并发多次 `/totp` 请求，每次后端都记 `totp_gen` 审计（审计流水被同一操作污染多条）；`remaining` 为负时 `:percentage` 也为负。修复：打开时置 `totpRemaining = 30` 再等首响应，或 interval 里加 in-flight 标志。

**[P3] Tokens.create() 无错误处理；令牌明文在弹窗关闭后仍驻留内存**（`web/src/views/Tokens.vue:88-101`）

- 创建失败无任何提示（unhandled rejection），与全站其它 create 模式不一致；`plaintext` ref 在「仅此一次显示」弹窗关闭后不清空，值保留到离开页面（服务端已保证不可再取，属前端内存卫生问题）。建议 dialog `@closed` 时 `plaintext.value = ''`。

**[P3] Audit.load() 无 catch 且翻页响应可乱序**（`web/src/views/Audit.vue:75-99`）

- `try { ... } finally` 缺 catch：请求失败无用户提示（unhandled rejection）；快速翻页/改 pageSize 时多个 `load()` 并发，后到的旧响应会覆盖新页数据（与 P2-4 同类，后端真分页下 offset 错位更明显）。补 catch + 请求序号即可。

**[P3] 审计动作中文标签表缺 10 种后端动作**（`web/src/views/Audit.vue:56-69`，对照 `internal/model/model.go:172-193`）

- 后端共定义 22 种 action，前端 `actions` 只映射 12 种；`template_delete`、`ai_chat_draft`、`ai_chat_config`、`user_create/update/delete`、`export`、`key_rotate`、`totp_gen`、`import` 均回退显示英文原始码，与整页中文标签不一致。建议与后端常量表对齐（或由后端下发 label 映射）。

**[P3] build/test 流水线不执行任何类型检查**（`web/package.json` scripts + `web/tsconfig.json`）

- tsconfig 已开 `strict: true`，但 scripts 只有 `vite build` / `vitest run`，devDependencies 无 `vue-tsc`，构建与测试都不跑类型检查 —— strict 对 CI 是空约束，类型回归只能靠编辑器偶然发现。加 `vue-tsc --noEmit` 到 build 前置即可（这是"检查没跑"，非泛泛的"建议开严格模式"）。

**[P3] markstream-vue 未显式固定 safe HTML 策略**（`web/src/components/ChatDrawer.vue:30`）

- 实测库默认 `htmlPolicy` 为 `'safe'`（`markstream-vue/dist/exports.js` 中 `?? "safe"`），净化实现完备（DANGEROUS_HTML_ATTRS、isUnsafeHtmlUrl、data: 白名单等）。但 ChatDrawer 未显式传该 prop，安全性依赖库的**默认值**；一次大版本升级改变默认即静默失去净化，而这里的渲染内容包含 AI 输出与库内条目数据（提示注入面）。建议显式 `:html-policy="'safe'"` 并在 lockfile 锁定版本。

**[P3] 四个页面各持一份同构 `fmt`/`fmtTime` 时间格式化**（EntriesList.vue:275、Audit.vue:102、Users.vue:126、Tokens.vue:118）

- 完全相同的 `v ? new Date(v).toLocaleString('zh-CN') : ...` 实现复制了 4 份；删除确认、加载-弹窗-保存三件套模式也重复 5-6 处。目前尚未失控（base.css 已统一页头/面板，模板选择卡片与 Templates 页同构），但 fmt 建议沉到 ui.ts，确认框可封装一个 `useConfirmAction`。属"重复已有苗头"，不是"已失控"。

**[P3] 静态资源服务无 CSP / X-Frame-Options / X-Content-Type-Options**（`cmd/keyhive/main.go:166-190`，前端关联项）

- spaHandler 只设置 Cache-Control。CSP 能把"渲染净化万一失守"的后果限制在应用内（自托管密钥管理器值得这层纵深），X-Frame-Options/X-Content-Type-Options 各一行成本。LAN HTTP 部署下 CSP 需兼容内联样式（Element Plus 大量内联 style），可用 `style-src 'unsafe-inline'` 起步。

**[P3] 无障碍：图标按钮与可点击卡片缺键盘/读屏语义**（EntryEdit.vue:102、Templates.vue:89 字段删除圆按钮无 aria-label；EntriesList.vue:26 移动端卡片整卡 @click 无 role/tabindex；el-table 行点击 open 不可键盘触达）

- 对比之下 Templates.vue:17 的 pick-card 做了 `role="button" tabindex="0" @keydown.enter`（团队已有正确范式），建议统一到卡片与危险图标按钮。App.vue 汉堡按钮已有 aria-label，是亮点。

## 做得好的地方

1. **XSS 面收得很干净**：全 `src/` 零 `v-html`/`innerHTML`/`dangerouslyUseHTMLString`（仅测试清理用到 innerHTML）；`entryURL()`（EntriesList.vue:178-186）对非 http(s) 值强制补 `https://` 前缀，从根上封死 `javascript:` 伪协议，且敏感/遮蔽字段不渲染链接，还有对应的负向测试。
2. **凭据存储设计正确**：会话是 HttpOnly + SameSite=Lax Cookie（API 层 `credentials: 'same-origin'`），localStorage 只存悬浮球位置与显隐两个无敏感键；登录、登出、改密后统一整页跳转清内存，登出先调 `/auth/logout` 失效服务端会话且失败也静默跳转（有测试覆盖）。
3. **遮蔽意识贯穿交互**：AI 草稿卡对敏感值只显示 `maskLen`（星号限长 8），`***` 语义为"保留库内原值"；Token 明文仅创建时一次性展示 + 复制失败降级提示手选；Settings 的 API Key 回显只显尾 4 位、留空不修改；EntryEdit 敏感字段渲染 password 输入。前端拿明文的全部路径（编辑 reveal、复制新建、明文导出）后端均记 `entry_reveal`/`export` 审计，链路可追溯。
4. **工程细节讲究**：store 用 window 自定义事件而非模块 ref，并写明"懒加载 chunk 双 store 实例"的根因注释；`api()` 401 且已在 /login 时不跳转防循环；store 层把后端 null 响应兜底成 `[]`（注释还标了回归版本 v1.3.6）；vitest.config 显式排除 ChatDrawer 并注明由后端 e2e + 浏览器实测兜底，理由诚实。
5. **SSE 解析实现正确**：按 `\n\n` 分帧、保留半包 buffer、`data:` 行 JSON.parse 失败静默跳过、401 单独处理、finally 里统一复位 streaming/loading（中途 return 也不会卡状态），流式光标与状态行 UI 细节完整。

## 测试盲区

- **ChatDrawer.vue 整体排除在覆盖之外**（vitest.config.ts:15 显式 exclude）：SSE 分帧/半包/事件分发、悬浮球拖拽与长按隐藏、messages 无上限增长（长会话内存）、卸载清理 —— 全部只靠人工。其中 `handleEvent` 各 case 与 history 截取逻辑其实是纯数据变换，抽成可测的纯函数即可低成本入测。
- **markstream-vue 渲染安全**：没有针对"AI 回复含 `<img onerror>` / `javascript:` 链接"的渲染断言，safe 策略是否持续生效完全依赖上游库（与 P3-8 呼应）。
- **竞态/时序**：无任何"慢响应乱序覆盖"类测试（EntriesList 搜索、Audit 翻页）；测试里所有 mock 都是立即 resolve，暴露不了 pending 交错问题。
- **fetch 超时/断流**：`api()` 无超时行为、SSE 中途断开（reader 抛错）后的 UI 状态，均未覆盖（后者在排除的 ChatDrawer 里）。
- **KeyboardEvent/指针事件细节**：Enter 发送（`.enter.exact`）、IME 组合输入回车、pointer capture 失败分支，jsdom 下均无法触达。
- **401 跳转真实行为**：api.test 只验证"分支进入"（jsdom 导航 no-op），整页跳转丢状态、`/login` 深链回跳等无 e2e 保障。
- **导入导出大数据量**：导出测试只断言下载链路触发，未覆盖超大库（Blob/内存压力）与 CSV 含引号/换行/逗号转义的解析展示路径。
