# 迁移指南

## v1.3.x → v1.4.0

**无需数据库迁移**（本轮无 schema 变更），替换二进制/镜像 + 浏览器强刷（Ctrl+F5）即可。以下行为变更请知悉：

### 1. 权限收紧（唯一新的 403 面）

`POST /api/v1/ai-config/test`（测试连接）与 `POST /api/v1/ai-config/models`（拉取模型列表）改为**仅管理员**可用。

- 背景：此前任意登录用户可借"空 Key 回退已保存管理员 Key"让服务器把真实 LLM Key 发往自填 URL（Key 外带 + SSRF，本轮 P0 修复）。
- 影响：普通用户在「设置」页点"测试连接/获取模型"会提示需要管理员权限（页面不崩）；管理员行为不变。
- 应对：多用户部署中需要自助测试连接的普通用户，请联系管理员代配。

### 2. Bitwarden 导入语义变化（安全收紧）

Bitwarden CSV 的 `notes` 字段导入后：**敏感字段（is_secret=true）+ AI 不可见（ai_visible=false）**，此前是非敏感且 AI 可见。

- 影响：导入的安全笔记（恢复码等）不再出现在 AI 条目列表，需要人工在 Web UI 查看/手动放开。
- 已导入的旧数据不受影响（不回溯）；介意的话可在编辑页手动调整这两个属性。

### 3. CSV 导入严格化

未闭合引号等畸形 CSV 此前会被静默"修复"（可能吞行/串列），现在**报错拒绝**。

- 应对：报错信息含原因；先用表格软件重新导出规范 CSV 再导入。

### 4. 空 master.key 拒绝启动

主密钥文件内容为空（0 字节/纯空白）时服务拒绝启动（防密钥退化为公开常量）。

- 应对：按日志提示删除该文件重启自动生成（**注意**：如果此前就是用空密钥文件启动过的旧实例，库内数据是用退化密钥加密的——请先用旧版本完整导出明文备份，再删 key 文件重启后导入）。

### 5. 请求体 10MB 上限

所有 API（含 AI 接口）请求体超 10MB 返回 413。正常条目/导入远小于此；如有超大单字段需求请先拆分。

### 6. 遮蔽导出改服务端

`--masked` / 遮蔽导出按钮现在请求 `?masked=1` 由**服务端**完成遮蔽（明文不再出进程），前端/CLI 保留双保险。输出内容与此前一致。

### 7. Anthropic 空 baseURL 端点修正

此前 Anthropic 协议不填地址时 Key 会被发往 OpenAI 兼容端点；现在默认官方 `api.anthropic.com`。已保存过"空地址"配置的，行为自动修正，无需操作。

### 8. CLI 行为变化

- `rotate-key`、`edit`、`export`、`import` 打错 flag：现在**退出码 2 且不发起任何请求**（此前 rotate-key 打错 flag 仍会执行轮换）。
- `import` 只登录一次（更快、审计更干净）。
- `export --masked` 走服务端遮蔽。

### 9. MCP

工具执行失败的结果现在带 `isError: true`（此前对 AI 客户端表现为正常文本）。ZCode/Claude 等客户端会正确识别失败并重试/报告。

### 10. 回滚

镜像 `latest` 换回固定版本号（如 `1.3.12`）即可。1.4.0 未改任何存储格式（密文仍为 `enc:v1:`），1.4.0 里新建/轮换过的数据旧版本可直接解密；唯一注意 1.4.0 导入的 Bitwarden 笔记在旧版本里仍按新语义存储（旧版界面也能正常显示，只是编辑页属性展示可能不同）。

---

## 升级操作

```bash
# Docker Run（群晖等）
docker pull registry.cn-hangzhou.aliyuncs.com/lpx03/keyhive:1.4.0
docker stop keyhive && docker rm keyhive
# 按原参数重新 docker run（卷不删数据无损）

# docker compose
KEYHIVE_VERSION=1.4.0 docker compose up -d --build

# 裸机
go build -o keyhive ./cmd/keyhive   # 前端改动已随仓库 web/dist 预构建
```

升级后浏览器强刷一次（index.html 入口 no-cache，一般自动生效）。
