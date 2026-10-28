# 全仓代码审查汇总（2026-10-28，基线 v1.3.12 / 0ffd905）

10 路并行审查全部完成，共 **107 项发现：P0×1、P1×8、P2×31、P3×67**。各路详细报告见同目录 `01-review-*.md`，本文件是去重后的汇总索引与修复范围划分。

## 1. 各路计数

| # | 范围 | 报告 | P0 | P1 | P2 | P3 |
|---|---|---|---:|---:|---:|---:|
| 1 | 入口与交付（cmd/version/CI/Docker） | 01-review-bootstrap.md | 0 | 0 | 5 | 6 |
| 2 | 数据基础（db/model/audit） | 01-review-data-foundation.md | 0 | 0 | 4 | 5 |
| 3 | 存储与模板（store/templates） | 01-review-store-templates.md | 0 | 3 | 3 | 6 |
| 4 | 加密与 TOTP（crypto/totp） | 01-review-crypto-totp.md | 0 | 1 | 2 | 3 |
| 5 | 认证（auth） | 01-review-auth.md | 0 | 0 | 1 | 7 |
| 6 | HTTP API（api） | 01-review-http-api.md | 1 | 1 | 4 | 6 |
| 7 | AI 集成（aichat/aiapi） | 01-review-ai-integration.md | 0 | 0 | 4 | 8 |
| 8 | CLI（cli） | 01-review-cli.md | 0 | 1 | 3 | 7 |
| 9 | MCP 与导入（mcpserver/importer/kdbtest） | 01-review-mcp-importer.md | 0 | 1 | 1 | 8 |
| 10 | 前端（web） | 01-review-frontend.md | 0 | 1 | 4 | 11 |
| | **合计** | | **1** | **8** | **31** | **67** |

## 2. 交叉主题（多路报告共同指向的根因）

1. **审计 detail 字符串拼接 JSON**（#2/#6/#7 三路独立发现）：`audit.Log` 的 detail 靠 fmt 拼接，title/字段名含 `"` 即写坏审计记录——取证底线可被普通用户破坏。修复方向：audit 包提供结构化 helper（json.Marshal）。
2. **请求体无大小限制 + Validate 无长度上限**（#2/#6）：未认证 `/auth/login` 可超大 JSON 耗内存。修复方向：全局 `http.MaxBytesReader` 中间件 + model 层长度校验。
3. **轮换（rotate）一致性缺陷群**（#3/#6/#8）：漏重加密 settings 中的 LLM API Key、无并发互斥（TOCTOU 留下旧钥密文）、CLI 15s 超时诱发二次轮换。
4. **错误信息泄漏**（#6）：~15 处 `writeErr(err.Error())` 回显 SQL 原文。
5. **交付链路断点**（#1）：CI 镜像从不传 `--build-arg VERSION`（ACR 镜像恒 dev）、无 `.dockerignore`（本地 `data/` 含 master.key 可能进构建上下文）、`/app/data` 未预创建属主（非 root + bind mount 首启崩溃）。
6. **前端健壮性**（#10）：无路由守卫、fetch/SSE 无超时无取消、搜索无防抖且存在乱序竞态。

## 3. P0 / P1 清单（第 3 步转复现测试，第 4 步修复）

| 级别 | 位置 | 问题 | 修复方向 |
|---|---|---|---|
| **P0** | internal/api/api.go:85 + aichat_api.go:341 | `/ai-config/test`、`/ai-config/models` 挂普通用户组且"空 Key 回退已存管理员 Key"：任意登录用户可让服务器把真实 LLM Key 发往任自填 URL（Key 外带 + SSRF 读原语，错误回显响应体 200 字符） | 收紧到 admin 组 + 不回退真实 Key + 限制回显 |
| P1 | internal/store/store.go:318 | 轮换漏重加密 settings 中用同一 Cipher 加密的 LLM API Key，轮换后 AI 助手必然失效 | RotateAllEntries 连同 settings 加密项一起重加密 |
| P1 | internal/store/store.go:318 + api/rotate.go:33 | 轮换全程与并发写无互斥：窗口内新建/更新条目留旧钥密文，永久不可解密且 handler 报 404 掩盖 | 轮换期间全局写互斥（进程内 mutex + 请求粒度） |
| P1 | internal/store/store.go:204 + :48 | 单条坏行使 ListEntries 整体失败拖垮全库列表/导出；且值本身以 `enc:v1:` 开头会被跳过加密原样入库制造坏行 | 加密前转义/校验前缀；列表对坏行容错（跳过+记日志） |
| P1 | internal/crypto/crypto.go:134 | 空 master.key 文件被静默接受，主密钥退化为 SHA-256("")，加密形同虚设 | 空文件直接报错拒绝启动 |
| P1 | internal/importer/importer.go:110 | Bitwarden 安全笔记导入设为非敏感 + ai_visible=true，恢复码等明文直达 AI 上下文绕过遮蔽与审计 | notes 默认 is_secret=true + ai_visible=false（行为变更，进迁移指南） |
| P1 | web EntriesList.vue:290 + api export | "遮蔽导出"是前端脱敏，后端 /export 恒返全库明文，传输层承诺不成立 | 后端支持 masked 参数（真·服务端遮蔽），前端/CLI 传参 |
| P1 | internal/cli/admincli.go:184 | import 循环逐条重新 admin 登录：N 条 = N 次 argon2id + N 个会话 + N 条审计 | 登录一次复用 client |

## 4. 本轮一并修复的 P2（机械性强、测试可锚定）

- MaxBytesReader 全局限制（api.go:32）
- VerifyPassword 畸形参数 panic → 返回 false（auth/password.go:59）
- anthropic.go:281 流 error 事件被吞 → 传播错误
- aichat.go:213 anthropic+空 baseURL 发往 OpenAI 端点 → 协议各自默认
- main.go http.Server 零超时 → ReadHeaderTimeout/IdleTimeout（不设 WriteTimeout 保 SSE）
- cli adminFlags 丢 Parse 错误 → 退出 2；login.go config.json 写后 chmod 0600
- mcp.go 工具错误补 isError 标记
- importer CSV：LazyQuotes 静默吞行 → 畸形行报错
- 交付：.dockerignore、Dockerfile /app/data 预创建属主、docker-image.yml 传 VERSION build-arg、release-binary.yml 权限降 job 级
- api writeErr 泄漏内部错误 → 通用文案 + 服务端日志（~15 处）
- audit detail 结构化（与交叉主题 1 合并处理）

## 5. 暂不修复、进重构方案（第 5 步 docs/review/03-refactor-plans.md）

会话绝对寿命/过期清理任务、cookie Secure 配置开关、key_check 强 KDF（argon2id 派生）、GCM AAD 绑定上下文、db seed 原子化、登录时序枚举、前端路由守卫/防抖/超时重构、SSRF 白名单开关、模型层长度上限体系、双协议抽象统一等——见各路报告 P2/P3 与重构方案文档。

## 6. 总体评价

全仓无框架级腐化：SQL 全参数化、令牌/会话只存哈希、AI 四条路径遮蔽+隐身无绕过、上游 Key 加密落库、前端零 v-html、CI 凭据卫生干净。发现集中在**边界条件与流程一致性**（轮换、导入、交付链路），符合"功能迭代快、边界打磨欠"的阶段性特征。P0 仅 1 项且依赖"普通登录用户"权限前提（内网单管理员部署下实际暴露面有限），但必须修。
