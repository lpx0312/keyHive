# 数据基础（db/model/audit）代码审查报告

> 审查人：代码审查员（安全 + 正确性 + 可维护性）
> 日期：2026-10-28　|　只读审查，未修改任何被审文件

## 概览

本次通读 `internal/db/`（db.go、util.go、db_test.go、db_extra_test.go）、`internal/model/`（model.go、model_test.go）、`internal/audit/`（audit.go、audit_test.go）共 7 个文件的全部内容，并交叉验证了 store/auth/api 等调用方以确认边界行为。

总体评价：数据基础层质量较高。SQL 全参数化（动态 WHERE 只拼固定片段、值走占位符），迁移幂等且配了旧库升级测试，时间统一 UTC+RFC3339 使字符串排序即时间排序，审计 detail 只记 title/字段名不记敏感明文。未发现 P0/P1 级问题。

主要缺口集中在四处 P2：seed 初始化非原子（中断后库可能永远打不开）、model 校验无任何长度上限、审计 detail 由字符串拼接构造（用户可控标题含引号即产生非法 JSON）、审计写入失败被完全静默吞掉（与"全程审计"承诺冲突且零可观测性）。另有单连接池 + 未来事务内调审计的潜在死锁陷阱等 5 条 P3。

发现计数：**P0 × 0　P1 × 0　P2 × 4　P3 × 5**

## 发现清单

### P2-1　[一般] 首次 seed 非原子，中断后 Open 永久失败

**位置**：`internal/db/db.go:196-243`

**问题**：`seed()` 中"INSERT admin 用户"（db.go:216）与"INSERT settings('seeded')"（db.go:242）是两次独立的 autocommit `d.Exec`，中间无事务包裹。若进程恰在这两步之间崩溃/被杀（首次启动窗口），下次 `Open` 时：seeded 标记不存在 → 重新执行 seed → `INSERT INTO users ... VALUES ('admin', ...)` 撞 `username UNIQUE` 约束 → migrate 返回错误 → **Open 永久失败，服务无法启动**，需手工改库才能恢复。两个进程同时首启也会出现同样冲突（一方 UNIQUE 失败退出，可重启恢复）。

**影响**：触发窗口极窄（仅首次初始化），但后果是库变砖、服务拒绝启动，且报错信息（UNIQUE constraint）不指向真实原因。

**修复建议**：用事务包裹 seed 全部写入（`d.Begin()` / `Commit`）；或至少把 admin 插入改为 `INSERT ... ON CONFLICT(username) DO NOTHING`，使 seed 可重入。补一个"admin 已存在但 seeded 缺失"的恢复测试。

---

### P2-2　[一般] Entry.Validate 无任何长度/数量上限

**位置**：`internal/model/model.go:90-117`

**问题**：`Validate()` 只校验空标题、空字段名、字段名重复、类型合法，对 **title/description/field key/value/tags 单值长度、字段数、标签数均无上限**。同时 API 层 `readBody`（internal/api/api.go:32-35）直接 `json.NewDecoder(r.Body).Decode(v)`，无 `http.MaxBytesReader`，认证用户可单请求写入任意大小数据。

**影响**：登录用户（或经 AI 草稿路径）可提交超长字段使 DB 单行无限膨胀：磁盘放大、`ListEntries` 的 `LIKE '%q%'` 全表扫描与全量解密变慢、AI 可见条目会把超长明文塞进 LLM 上下文（成本放大）。对多用户部署，这是低成本 DoS 面。

**修复建议**：在 `Validate()` 中定义上限（示例：title ≤ 200、description ≤ 5000、field key ≤ 100、value ≤ 64KiB、fields ≤ 50、tags ≤ 20），超限返回明确错误；HTTP 层统一加 `http.MaxBytesReader`（如 2MiB）。

---

### P2-3　[一般] 审计 detail 由字符串拼接构造，用户可控标题含引号即产生非法 JSON

**位置**：`internal/audit/audit.go:12-19`（API 设计根源）；调用点 `internal/api/api.go:263,279,301,320`、`internal/api/aichat_api.go:195,294`、`internal/api/webui.go:60` 等

**问题**：`audit.Log` 的 `detail` 参数按 JSON 契约设计（空值自动补 `{}`，audit.go:13-15），但所有调用方都用原始字符串拼接构造：

```go
audit.Log(..., model.ActionEntryCreate, &id,
    `{"title":"`+e.Title+`"}`, clientIP(r))   // api.go:263-264
```

条目标题、AI 草稿标题/分类均为用户可控，含 `"` 或 `\` 时（如标题 `我的"密码"库`）写入的 detail 即为**非法 JSON**。当前 Web UI 的 Audit.vue:29 将 detail 按纯文本展示未崩溃，但任何后续消费（导出分析、SIEM 对接、前端格式化）遇到坏 JSON 即断裂。

**影响**：审计数据质量问题，静默发生、无告警，破坏审计记录的结构化契约；修复成本极低。

**修复建议**：`audit.Log` 增加结构化入口（如 `detail map[string]any` 或辅助函数 `audit.Detail(pairs ...) string`，内部 `json.Marshal`），逐步替换各调用点的手拼字符串。

---

### P2-4　[一般] audit.Log 完全吞掉写入错误，失败零可观测

**位置**：`internal/audit/audit.go:16-18`

**问题**：`db.Exec(...)` 的返回值被整体丢弃，连 `log.Printf` 都没有。"审计失败不阻断主流程"的取舍本身合理（注释也声明了），但**完全无观测**意味着：磁盘满、audit_logs 表损坏、迁移异常等场景下审计记录静默丢失，事后无法察觉。对一个以"全程审计"为核心安全承诺的密钥管理器，静默漏记是声誉性风险。

**影响**：安全事件发生时可能出现"reveal 动作无审计记录"且无任何痕迹，削弱审计作为追责依据的可信度。

**修复建议**：最小修复——失败时 `log.Printf("审计写入失败: %v", err)`；可选进阶——连续失败次数指标（暴露到 /version 或健康端点）。

---

### P3-5　[建议] pragma table_info 遍历后未检查 rows.Err()

**位置**：`internal/db/db.go:125-137`、`internal/db/db.go:169-185`

**问题**：`migrateUsersColumns` / `migrateEntriesColumns` 的 `for rows.Next()` 循环结束后直接 `rows.Close()`，未检查 `rows.Err()`。若迭代中途出错提前终止，`existing` 列名集合不完整，可能对已存在列执行 `ALTER TABLE ADD COLUMN` → 报"duplicate column"→ Open 失败，错误信息误导排查方向。

**修复建议**：循环后加 `if err := rows.Err(); err != nil { return err }`（两处各一行）。

---

### P3-6　[建议] APIToken.Effective() 忽略 ExpiresAt，语义陷阱

**位置**：`internal/model/model.go:153-155`

**问题**：`Effective()` 只判断 `RevokedAt == nil`，不看 `ExpiresAt`。名字暗示"整体有效性"，实际的过期校验在 `auth.GetAPIToken`（internal/auth/store.go:168-172）里完成。当前 `Effective()` 仅被测试调用，但未来调用方（如 CLI、导出逻辑）一旦信任该方法，过期令牌会被视为有效。

**修复建议**：在 `Effective()` 内补过期判断（解析 `ExpiresAt` 与 `time.Now().UTC()` 比较），或改名为 `NotRevoked()` / 直接删除以免误用。

---

### P3-7　[建议] 审计索引与主查询路径不匹配

**位置**：`internal/db/db.go:98`（索引定义）vs `internal/audit/audit.go:39-47`（实际查询）

**问题**：唯一审计索引 `idx_audit_created(created_at DESC)` 当前**没有任何查询使用**：`List` 排序用 `ORDER BY id DESC`（rowid 天然有序，无需索引），`Count`/`List` 的 `action = ?` 筛选无索引支撑（全表扫）。数据量增长到数万条后，按 action 筛选会线性变慢。created_at 索引也支撑不了排序（排序键是 id）。

**修复建议**：按真实查询路径建 `(action, id DESC)` 复合索引；若 created_at 索引是为未来时间范围查询预留，请注释说明，否则删除以省去写放大。

---

### P3-8　[建议] nowUTC 四处重复实现，时间格式契约无单一权威

**位置**：`internal/db/util.go:10`、`internal/audit/audit.go:9`、`internal/store/store.go:36`、`internal/api/users.go:16`

**问题**：`func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }` 在 4 个包各自复制。当前完全一致（好），但整个系统的时间正确性依赖"所有写入点统一 UTC+RFC3339"这一隐式约定——字符串字典序 = 时间序支撑着 `ORDER BY updated_at DESC`（store.go:196）、轮换 stale 计算、审计时间线。将来任何一处单独改成本地时区或不同格式，排序/比较会**静默**错乱，没有编译期或测试期信号。

**修复建议**：收敛为单一导出函数（如 `model.NowUTC()` 或 `db.NowUTC()`），其余引用之；可加一个"全库 created_at 均以 Z 结尾且可被 RFC3339 解析"的扫描测试钉住契约。

---

### P3-9　[建议] audit_logs 只增不删，无保留/归档策略

**位置**：`internal/db/db.go:87-98`

**问题**：审计表纯追加，全代码库无任何 `DELETE FROM audit_logs`/保留天数设置（settings 表也未预留）。自托管低写入量下短期无碍，但长期运行（含 login/login_failed 高频动作、暴力尝试场景下每请求一条）会让库文件与 WAL 持续膨胀，且审计页分页查询变慢。

**修复建议**：提供可选的保留天数设置（如 settings 表 `audit_retention_days`，0 = 永久），启动或定时清理过期记录。安全存证需求下默认永久、仅提供选项也可接受。

---

### 范围外关联观察（供参考，不计入本范围发现）

- **clientIP 信任 X-Forwarded-For**（internal/api/api.go:37-42）：可被客户端伪造，直接进入审计记录的 ip 字段，削弱审计 IP 的取证可信度。注释表明是有意设计（内网直连），若上反代建议改为可配置信任开关。
- **users.go 的 SET 子句拼接**（internal/api/users.go:161）：`UPDATE users SET `+strings.Join(sets, ", ")——sets 元素全是硬编码白名单字面量，值走占位符，当前安全；属"安全场景的字符串拼接"，建议保留注释防止后人往 sets 里加入用户可控片段。
- **仓库无 go test 的 CI workflow**（.github/workflows 仅 docker-image.yml、release-binary.yml）：测试只在 Windows 本机跑，见测试盲区第 2 条。

## 做得好的地方

1. **SQL 纪律好**：范围内 `audit.Count`/`audit.List` 的动态 WHERE 只拼接固定 SQL 片段，用户值全部走 `?` 占位符（audit.go:24-47）；范围内无任何用户输入进入 SQL 文本。
2. **迁移幂等 + 测试扎实**：建表全部 `IF NOT EXISTS`，补列用 `pragma table_info` 守卫，seed 用标记防重；db_test.go / db_extra_test.go 覆盖了 v1.3.0 无 tags 旧库、无 is_admin 旧库、优先提升 admin 命名账号、空 users 表、二次 Open 不重种、损坏库报错等真实升级路径。
3. **时间处理统一**：所有写入点一致 UTC+RFC3339（db/audit/store/users 四处），字符串排序即时间序；审计排序用 `ORDER BY id DESC` 规避了同秒时间戳的排序歧义。
4. **审计不泄漏敏感明文**：entry_reveal / field_reveal / totp_gen 的 detail 只记 title 与字段名，不记被 reveal 的值（api.go:279、aiapi.go:126、webui.go:60）；export 只记条数（api.go:122）；登录失败不区分"用户不存在/密码错"，防用户枚举（api.go:145-147）。
5. **SQLite 配置与事务边界正确**：WAL + foreign_keys + busy_timeout(5000) + synchronous(NORMAL) + 单连接串行化，对单进程纯 Go SQLite 是教科书式组合；`RotateAllEntries` 先预计算、再单事务落库、成功后才换内存密钥，失败回滚旧密钥仍有效（store.go:316-389），且事务在 audit 调用前已提交，避开了单连接池死锁。

## 测试盲区

- **审计失败路径零覆盖**：audit.Log 错误被吞（P2-4），测试只覆盖成功写入；无法发现"静默漏记"。
- **测试套件绑定 Windows 且无 CI**：仓库无跑 `go test` 的 workflow；`TestOpenBadPath`（db_test.go:104）依赖 `NUL\:\invalid|path` 在 Windows 非法，`|` 和 `:` 在 Linux 是合法文件名字符，该用例在 Linux 会假失败——迁移代码号称跨平台（Docker 镜像跑 Linux），但迁移测试未在 Linux 验证过。
- **seed 中断恢复无测试**：P2-1 的"admin 已插、seeded 未写"场景没有用例守护（修复时应补）。
- **审计 detail 特殊字符无测试**：现有测试的 title/detail 均为纯 ASCII 简单值，含引号/反斜杠的标题不会触发 P2-3 的 JSON 损坏。
- **并发场景无测试**：单连接池下"事务活跃期间调用池查询（如 audit.Log）会永久阻塞"这一结构性风险，目前无代码触发也无回归测试防护，未来重构易踩中。
- **大数据量行为未验证**：万级审计/条目下 Count/List/LIKE 的性能、WAL 膨胀无基准或压力测试（对应 P3-7、P3-9）。
- **双进程首启竞态**：两个实例同时首次 Open 同一库文件的 seed 竞态无测试（P2-1 的并发变体）。
