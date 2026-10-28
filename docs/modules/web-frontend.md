# 前端（web/）

> 源码 `web/src/`，构建产物 `web/dist/` 由 `web/web.go` 的 `//go:embed all:dist` 嵌进 Go 单二进制；
> 改前端后必须重新 `npm run build` + 重新编译/重启服务才会生效（embed 是编译期快照）。

## 职责与技术栈（Vue3/Element Plus/markstream-vue/Vite/Vitest）

- Vue 3.5（`<script setup>` + TypeScript 5.7）+ vue-router 4（懒加载路由）+ Element Plus 2.9（zh-cn locale、@element-plus/icons-vue）
- markstream-vue 2.0.14：AI 助手 SSE 流式 Markdown 渲染（`MarkdownRender` 组件 + `markstream-vue/index.css`）
- Vite 6（`vite.config.ts`：dev 端口 5173，`/api` 代理 `http://localhost:8020`；build 输出 `dist/`，`emptyOutDir: true`）
- Vitest 5 + jsdom + @vue/test-utils + @vitest/coverage-v8（npm scripts：`dev` / `build` / `test` / `test:watch` / `test:cover`）
- 本地开发循环：`cd web && npm run dev`（5173 端口，API 代理到本机 8020 的 `keyhive serve`）；
  提交前 `npm run test:cover` 守住四维 90% 阈值

## 目录结构

| 文件 | 职责 |
|---|---|
| `src/main.ts` | 入口：createWebHistory 路由表（views 全部懒加载）+ 全局装 ElementPlus(zhCn) + base.css |
| `src/App.vue` | 布局壳：桌面侧栏 / 移动顶栏 + 抽屉菜单；管理员菜单按 `me.isAdmin` 显隐；退出登录；挂 ChatDrawer |
| `src/index.html`、`src/env.d.ts` | SPA 模板（`#app` 挂载点、favicon.svg、zh-CN）与 `*.vue` 模块类型声明 |
| `src/api.ts` | fetch 封装（`/api/v1` 前缀、`credentials: 'same-origin'`、401 整页跳 `/login`、非 2xx 抛 `data.error`）+ Entry/Field/Template/APIToken/User/AuditLog 接口类型 |
| `src/store.ts` | 全局登录态 `me`/`loadMe()`；跨 chunk 事件 `ENTRIES_CHANGED_EVENT`、`FAB_HIDDEN_EVENT` + `setFabHidden()` |
| `src/ui.ts` | `useIsMobile()`（断点 768px，resize 监听随组件卸载移除）；`copyText()`（安全上下文用 clipboard，HTTP 局域网降级 execCommand） |
| `src/base.css` | 全站设计语言基座：page-head / panel / group-* 公用样式（与模板页同构） |
| `src/views/` | 八个页面（见「页面清单」） |
| `src/components/ChatDrawer.vue` | AI 录入助手：可拖动悬浮球（长按 600ms/右键隐藏）+ 抽屉聊天（可全屏）+ SSE 消费 + 草稿确认入库 |
| `src/test/fixtures.ts` | 测试 mock 数据（mockEntries/Templates/Tokens/Users/AuditLogs/mockMe，与后端响应结构对齐） |

views/ 八页面一句话（路由：`/login`、`/entries`、`/entries/new`、`/entries/:id/edit`、`/templates`、`/tokens`、`/audit`、`/users`、`/settings`，`/` 重定向 `/entries`）：

- **Login.vue** 登录（成功后路由内跳转不整页重载，补跑 `loadMe()` 填充侧栏）
- **EntriesList.vue** 条目列表（搜索/分类/标签筛选、本地分页、批量删除、CSV/JSON 导入导出、URL 列提取、轮换提醒）
- **EntryEdit.vue** 新建/编辑（模板选择页、字段行、标签、TOTP 动态码弹窗、`?from=` 复制新建、另存为模板）
- **Templates.vue** 录入模板（分组卡片网格 + 关键词搜索 + 新增/编辑/删除弹窗）
- **Tokens.vue** AI 令牌（按 scopes read/search/reveal + 过期时间创建，明文仅显示一次，吊销）
- **Audit.vue** 审计日志（action 筛选 + 服务端分页，12 种动作中文标签）
- **Users.vue** 用户管理（创建 / 重置密码 / 禁用 / 管理员切换 / 删除）
- **Settings.vue** 设置（改密、悬浮球开关、AI 助手配置与测试连接、轮换阈值、主密钥轮换、页脚版本号）

## 与后端的契约

- **API 基址**：同源相对路径 `/api/v1`（`api.ts` 统一拼接）；dev 走 Vite 代理（5173 → 8020），生产前后端同端口（embed）
- **会话 Cookie**：登录成功 `Set-Cookie keyhive_session`（`auth.CookieName`）；所有请求 `credentials: 'same-origin'`；401 由 `api()` 统一整页跳 `/login`（SSE 请求在 ChatDrawer 内单独判 401）
- **SSE 端点**：`POST /api/v1/ai-chat/stream`（ChatDrawer 直连 fetch 消费 `res.body.getReader()`；帧格式 `data: {...}\n\n`，事件类型 `status` / `reply_delta` / `draft` / `error` / `done`；服务端因此不设 WriteTimeout，见 main.go 注释）
- **遮蔽语义 `***`**：
  - 敏感字段（`is_secret`）在列表 / AI read 通道 / 已保存 AI Key 回显中，value 恒为 `***`
  - 遮蔽导出走 `GET /export?masked=1`（服务端脱敏，明文不出服务端）；前端拿到结果后再对 `is_secret` 字段循环置 `***` 作客户端双保险（防服务端契约未生效时漏明文）
  - keyhive 格式恢复导入时遮蔽版 JSON 被后端拒绝（导入弹窗有警示文案）
  - ChatDrawer 草稿中敏感值回传 `***` 表示「保留库中原值」，后端不覆盖
- **分页参数**：条目列表一次全量拉取后本地切片（默认 15/页，5/10/15/20/50 可选，标签筛选与总数统计依赖全量数据）；审计页走服务端分页 `GET /audit?limit=&offset=&with_total=1`（默认 15/页）
- **明文取用**：编辑 / 复制时 `GET /entries/:id?reveal=true`（服务端记 entry_reveal 审计）；TOTP 码 `POST /entries/:id/totp`（服务端生成 30s 窗口，密钥不出库，记审计）

## 页面清单（核心交互 + 依赖端点）

| 页面 | 核心交互 | API 端点 |
|---|---|---|
| Login | 登录、初始密码提示 | `POST /auth/login`、`GET /auth/me` |
| EntriesList | 搜索连发防竞态、批量删除、导入 dry_run 预览、双模式导出、标签本地过滤 | `GET /entries?q=&category=`、`GET /categories`、`DELETE /entries/:id`、`GET /export`（遮蔽版 `?masked=1`）、`POST /import`、`GET /settings/stale-days` |
| EntryEdit | 模板骨架带入、敏感字段加密提交、TOTP 倒计时自动续窗口 | `GET /templates`、`GET /categories`、`GET /entries/:id?reveal=true`、`POST/PUT /entries`、`POST /entries/:id/totp`、`POST /entries/:id/save-as-template` |
| Templates | 分组浏览 / 深拷贝编辑不污染列表 | `GET/POST/PUT/DELETE /templates` |
| Tokens | scopes 勾选、过期时间、明文一次性展示 + 复制 | `GET/POST /tokens`、`DELETE /tokens/:id` |
| Audit | 动作筛选、翻页钳制末页 | `GET /audit` |
| Users | 密码重置踢会话、禁用/升降管理员 | `GET/POST /users`、`PUT/DELETE /users/:id` |
| Settings | 改密失败弹错误、AI 预设回显、主密钥轮换结果展示 | `POST /auth/logout`、`POST /password`、`GET/PUT /ai-config`、`POST /ai-config/models`、`POST /ai-config/test`、`GET/PUT /settings/stale-days`、`POST /rotate-key`、`GET /version` |

（ChatDrawer：`POST /ai-chat/stream` + 草稿确认复用 `POST /entries` / `PUT /entries/:id`，`***` 敏感值由后端保留原值）

## 安全与不变量

- **零 v-html**：Markdown 由 markstream-vue 组件承接，全仓无 v-html / innerHTML 写入
- **entryURL https 强制**：列表「地址」列只取非敏感字段（优先 `type=url`，其次按 URL_KEYS 常见 key 名 url/host/registry/endpoint… 匹配），`***` 值不渲染链接；无 `https?://` 前缀自动补 `https://`；链接统一 `target="_blank" rel="noopener"`
- **localStorage 不放敏感值**：仅 `kh-fab-hidden`、`kh-fab-pos` 两个 UI 偏好（悬浮球显隐 / 拖动位置）
- **明文仅限授权场景**：
  - EntryEdit 编辑 / 复制新建时拉取 `?reveal=true`
  - Tokens 创建令牌后一次性展示（可复制），列表只存哈希对应形态
  - 用户主动选明文导出（弹窗内警示 + 后端记审计）
  - ChatDrawer 草稿卡敏感值永远遮蔽：`***` 显示「保留库中原值」，新值用 maskLen（最多 8 个 `*`）表示长度
- **剪贴板降级**：`copyText()` 仅在安全上下文（HTTPS/localhost）用 `navigator.clipboard`，HTTP 局域网自动降级 `execCommand`，失败返回 false 由调用方提示手动复制

## 状态管理与竞态纪律

- `store.ts` 是模块级单例（`me` reactive，`loadMe()` 失败静默——401 已由 `api()` 跳登录）；**跨组件通知一律用 window 自定义事件而非共享 ref**——懒加载 chunk 可能各自内联一份 store 导致双实例（store.ts 注释明确约定：条目刷新用 `ENTRIES_CHANGED_EVENT`、悬浮球显隐用 `FAB_HIDDEN_EVENT`，EntriesList 与 ChatDrawer 监听并在 onUnmounted 清理）
- **请求序号模式**（EntriesList `load()`）：
  1. 发起时 `const seq = ++loadSeq` 记下本次序号
  2. 响应回来先判 `seq !== loadSeq` → 过期则整段 return，不赋值 entries
  3. 报错弹窗、loading 复位同样只归最新序号管（过期请求的报错不弹，避免旧失败覆盖新成功的界面）
  4. 效果：搜索/筛选连发时，后发快请求的结果不会被先发慢响应覆盖（审查 P2 竞态的修复）
- 其余约定：登录页成功后路由内跳转（`router.push('/entries')`）避免整页重载；删除/导入后总页数变少时页码钳到最后一页

## 测试体系

- `vitest.config.ts`：include `src/**/*.test.ts`；coverage 全量口径（`src/**/*.ts` + `*.vue`），exclude 测试自身 / `src/test/**` / `env.d.ts` / `main.ts` / `ChatDrawer.vue`（SSE 流解析 + 悬浮球拖拽 jsdom 下不可有效覆盖，数据链路由后端 e2e 与浏览器实测保障，行为回归由 ChatDrawer.test.ts 保证）；thresholds lines/functions/statements/branches **全部 90**；reporter text + json-summary
- 现状 13 个 .test.ts、141 个用例；fixtures.ts 提供 mockEntries / mockTemplates / mockTokens / mockUsers / mockAuditLogs / mockMe 五套数据 + 登录态
- 测试文件分布：api / store / ui / App 各一个逻辑层文件；views 八页各一个（Login 3、EntryEdit 22、EntriesList 22、Settings 21、Templates 11、Tokens 9、Users 9、Audit 7）；ChatDrawer 8（悬浮球显隐、SSE 主链路、草稿入库、清空对话、error 帧）
- 已知坑：
  - `vi.mock('../ui')` 必须返回真 `ref()`——普通对象不参与模板自动解包，移动端分支测不出来（EntriesList/EntryEdit/Audit.test.ts 均注明「必须真 ref」）
  - Element Plus 弹层（el-drawer `append-to-body` / el-select options）teleport 到 body：每个 beforeEach 要 `document.body.innerHTML = ''` 清残留，断言去 `document.body` / `document.querySelector` 找节点，抽屉渲染异步用 `vi.waitFor` 等标题出现
  - 两个原 `test.fails` 锚点（搜索乱序竞态、改密零反馈）修复后已转常规绿测：EntriesList.test.ts「搜索竞态：后发快请求的结果不应被先发慢请求的旧结果覆盖」、Settings.test.ts「修改密码失败（旧密码错误）应向用户展示错误提示」——写新回归测时优先常规 `it()`，`test.fails` 只留作缺陷尚未修复时的红锚点
  - jsdom 未实现 PointerEvent / Pointer Capture：ChatDrawer.test.ts 用 MouseEvent 派发 + 原型打桩 setPointerCapture/releasePointerCapture，afterEach 删除桩
