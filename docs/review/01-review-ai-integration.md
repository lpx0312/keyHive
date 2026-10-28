# AI 集成（aichat/aiapi）代码审查报告

审查日期：2026-10-28　|　审查方式：只读通读全部源码与测试（含依赖包 model/store/audit/auth 及挂载层 api/aichat_api.go、cmd/keyhive/main.go 的数据流核对）

## 概览

- 范围：`internal/aiapi/`（3 文件）+ `internal/aichat/`（12 文件），共 15 文件约 2800 行；两包测试当前全部通过。
- 总体评价：**aiapi 的遮蔽与隐身实现是正确的**——ListEntries/GetEntry/Search/Reveal 四条路径全部先过滤 `ai_visible=false` 再 `MaskSecrets()`，未发现任何绕过；scope 三级分离在路由层（main.go）按端点强制，reveal 全程审计且只记字段名不记值。
- aichat 双协议适配整体健壮：SSE 解析有行缓冲上限、畸形 chunk 跳过、tool_calls 分片还原两协议均正确，上游 API key 全链路（存储加密、回显遮蔽、仅进请求头）处理干净。
- 主要问题集中在**错误路径**：Anthropic 流式 error 事件被静默吞掉（用户拿到空回复假成功）、审计 detail 手工拼接 JSON 可产生畸形/被伪造、连通性测试端点对所有登录用户开放并回显任意 URL 的响应片段（SSRF 面）、anthropic 协议的默认 BaseURL 被 OpenAI 默认值遮蔽成死代码。
- 无 P0/P1 级发现。

## 发现清单

分级：P0 阻断 / P1 严重 / P2 一般 / P3 建议。**P0：0 条，P1：0 条。**

### P2（4 条）

**[P2] Anthropic 流式 error 事件被静默吞掉，失败表现为"空回复 + 成功"**（internal/aichat/anthropic.go:281-309）
`parseAnthropicStream` 的事件 switch 只有 `content_block_start` / `content_block_delta` 两个 case、无 default 兜底。Anthropic 官方流在中途出错（限流、credit 不足、overload）会发送 `{"type":"error",...}` 事件，此处被直接忽略；流随后正常 EOF，函数返回 content 为空的 choice，`ChatStream`（stream.go:49-52）因无 tool_calls 而把它当正常回复，`Reply=""` 成功返回，前端收到空回复与 done 事件，无任何错误提示。同理 OpenAI 协议下流中 data chunk 若为错误对象（`{"error":{...}}`，choices 为空）在 stream.go:160 也被 `continue` 跳过。附带影响：`message_delta` 中的 stop_reason（如 max_tokens 截断）同样未读取，截断被当正常完成。
修复：switch 增加 `case "error": return nil, fmt.Errorf(...)`（OpenAI 侧对无法解析为 chunk 的 payload 至少记录原始内容）；读取 `message_delta.stop_reason`，非 end_turn/tool_use 时返回明确错误。

**[P2] reveal 审计 detail 手工拼接 JSON，title/字段名含引号即畸形、可伪造**（internal/aiapi/aiapi.go:126-127）
```go
audit.Log(..., `{"title":"`+e.Title+`","field":"`+f.Key+`"}`, r.RemoteAddr)
```
`Entry.Validate()` 对 Title 仅 TrimSpace、对 Field.Key 无字符限制，两者均可含 `"` 与 `\`。title 为 `a"b` 时 detail 是无效 JSON（审计页解析失败）；title 为 `x","field":"password2` 时可伪造审计中显示的字段名——field_reveal 审计是"AI 取走了哪个敏感字段"的唯一追责凭据，可被条目创建者预先污染。同根因模式还出现在挂载层（aichat_api.go:190、289 草稿审计；api.go:264、280、302 等），属系统性问题，本范围内先修 aiapi 这处。
修复：`json.Marshal(map[string]string{"title": e.Title, "field": f.Key})`。

**[P2] 连通性测试/模型列表端点对所有登录用户开放，任意 baseURL + 响应体回显构成 SSRF 读原语**（internal/api/api.go:85-86 挂载；internal/aichat/models.go:59、aichat.go:362 数据流）
`POST /ai-config/test` 与 `POST /ai-config/models` 挂在普通登录组（写入配置的 `PUT /ai-config` 在 api.go:110 是 admin 组），且 handler（aichat_api.go:329-397）接受请求体里的任意 `base_url`：`fetchModelsOnce` 对非 200 响应返回 `HTTP %d: %s` 并附响应体前 200 字节，`callLLM` 同样把上游响应体截断 200 字节放进错误信息，TestConnection 再原样返回。即任何普通登录用户可让服务器向任意内网 URL 发 GET（及 Chat 的 POST），并通过错误信息读到该 URL 响应体片段，用于内网探测与指纹识别。http.Client 默认跟随重定向（≤10 跳），也无内网地址限制。
权衡评估：baseURL 用户自配是产品核心能力（15 个预设 + 本机 Ollama/LM Studio），服务本身定位内网自托管、登录者即受信雇员，单用户/小团队部署下风险可接受；但"改配置要 admin、用任意 URL 发请求并读响应不要 admin"是权限不对称，多用户部署时应收敛。建议：将这两个端点挪入 admin 组，或仅允许对 admin 已保存的 baseURL 发起测试/拉模型；保持现状也应在文档明示该边界。重定向与内网段黑名单在内网自托管定位下可不加。

**[P2] anthropic 协议的默认 BaseURL 是死代码，空 baseURL 时会拿 OpenAI 端点 + Anthropic 头去请求**（internal/aichat/aichat.go:213-215 → anthropic.go:189-191）
`Chat`/`ChatStream` 入口先把空 BaseURL 填成 `DefaultBaseURL`（智谱 OpenAI 端点 `https://open.bigmodel.cn/api/paas/v4`），随后 `callLLM` 才按协议分派到 `callAnthropic`，其 `if cfg.BaseURL == ""` 默认 `https://api.anthropic.com` 永远不会触发（models.go:18-20 → 107-109 同根因，`listModelsAnthropic` 的默认同样死代码）。后果：`protocol=anthropic` 且 baseURL 为空时，请求发往智谱 OpenAI 端点的 `/v1/messages`——不仅必然 404，还会把用户的 `x-api-key`（可能是 Anthropic 官方 key）发给错误的服务商。当前 UI 路径被挂载层 `orDefault` 双层兜底掩盖（saved.BaseURL 永不为空），仅直接调用包 API（CLI/第三方/测试）时触发，故定 P2。
修复：默认值按协议区分——入口处 `if cfg.BaseURL == "" { cfg.BaseURL = ProtocolDefault(cfg.ProtocolOrDefault()) }`。

### P3（8 条）

**[P3] reveal 不限制 is_secret 字段**（internal/aiapi/aiapi.go:118）
`FieldByKey` 查到任意字段即返回明文，无 `f.IsSecret` 检查。非敏感字段本就对 read scope 明文可见，无实际泄露，但"reveal scope = 取敏感字段明文"的语义被稀释（非 secret 字段也走审计与 404 探测面）。建议 `if !f.IsSecret { writeErr(w, 400, "该字段非敏感字段") }`。

**[P3] 上游响应读取上限与错误解析两条路径不一致**（internal/aichat/aichat.go:351 vs anthropic.go:223、230）
非流式 `callLLM` 对 200 响应 `io.ReadAll` 无上限（callAnthropic 有 8MB LimitReader），异常/恶意上游可无限耗内存；流式路径非 200 时（stream.go:124-126）单次 `Read` 4096 字节且不解析 `error.message`（callLLM:352-363 会解析），同一故障两协议两路径给出不同粒度的报错。建议统一为 LimitReader + 统一错误提取函数。

**[P3] 测试连接强制携带 tools schema 且无总超时**（internal/aichat/aichat.go:334-336、testconn.go:19）
`callLLM` 硬编码 `Tools: toolsSchema()`，`TestConnection` 无法关闭：对 "ping" 消息，部分模型可能返回 tool_calls，进入 agent 循环（空回调回 `{"error":"查询不可用"}`）最多 4 轮后以"工具调用超过 4 轮"误报连接失败；单轮 60s × 4 轮，测试最长可挂 4 分钟（仅靠 r.Context() 兜底）。建议 TestConnection 走无 tools 的最小请求并收紧总超时。

**[P3] 回传 LLM 的工具结果错误 JSON 未转义**（internal/aichat/aichat.go:275）
`return `{"error":"` + err.Error() + `"}`, false, nil`——err 含 `"` 时产生畸形 JSON 发给上游模型（无安全问题，但模型可能误读）。建议 `json.Marshal`。

**[P3] 遮蔽/隐身契约完全依赖调用方自觉，包内无兜底**（internal/aichat/aichat.go:273-281）
`execTool` 把 `SearchEntries` 回调返回的条目原样 marshal 进 prompt，"必须已遮蔽、已过滤 ai_visible=false"仅是注释契约（当前唯一调用方 aichat_api.go:164-180 实现正确）。未来新增调用方（如 CLI、MCP）漏掉遮蔽即静默泄露。建议在 aichat 包内统一做 `MaskSecrets()` + `AIVisible` 过滤兜底，一行防御消除整类风险。

**[P3] search_entries 空 query = 全量条目结构进 prompt**（internal/aichat/aichat.go:117-118、164-180）
工具描述明确"空串=列出全部"，挂载层回调对空 query 返回全部可见条目（含全部字段名与注释）一次性进 LLM 上下文：条目量大时 token 成本失控、可能超出模型上下文导致请求失败。建议在回调或 execTool 处加条数上限（如 50 条并截断字段注释）。

**[P3] Chat 与 ChatStream 工具循环重复实现且已出现行为漂移**（internal/aichat/aichat.go:227-260 vs stream.go:44-81）
两段 90% 相同的循环需双份维护，已有一处漂移：非流式 `res.Reply = strings.TrimSpace(...)`（aichat.go:233），流式 `res.Reply = choice.Message.Content` 未 TrimSpace（stream.go:50），同一上游响应两接口返回的 Reply 不同。建议抽取共用循环，差异只在 LLM 调用函数。

**[P3] aiapi 无分页，Store 错误原文回显给 token 持有方**（internal/aiapi/aiapi.go:55-58、88-91）
ListEntries/Search 返回全量可见条目（无分页参数），条目多时响应大；出错时 `writeErr(500, err.Error())` 把底层错误（含 SQLite/解密细节如"字段 \"x\" 解密失败"）直接给 AI 客户端。token 持有者已是授权方，风险低，建议统一为通用错误文案。

## 做得好的地方

1. **遮蔽与隐身的正交实现正确且不可绕过**：四条路径全部"先 `AIVisible` 过滤、后 `MaskSecrets`"，隐身条目按 ID 直访、reveal 均 404，搜索路径同样过滤；敏感值在库中是密文（`enc:v1:...`），SQL `fields LIKE` 天然搜不到，非敏感明文可搜是注释明确的设计决策（store.go:187）。
2. **reveal 的授权与审计设计克制**：scope 三级（read/search/reveal）在路由层按端点强制挂载（main.go:122-135），审计只记 title + 字段名、绝不记明文值（与 ActionFieldReveal 语义严格一致），401/403/隐身 404 均有测试覆盖。
3. **上游 API key 全链路干净**：落库经主密钥加密（aichat_api.go:108-114）、GET 回显只露尾 4 位（maskedKey）、key 只进 Authorization/x-api-key 头从不进 URL、访问日志只记方法与路径、错误信息不含 key；`****` 哨兵避免了保存时回显值被当新 key 重存。
4. **SSE 解析的健壮性细节到位**：64KB/1MB 行缓冲上限（防上游超长行撑爆内存）、畸形 chunk 跳过、`[DONE]` 与纯 EOF 均能收尾、tool_calls 按 index 分片累积在 OpenAI（arguments 分片）与 Anthropic（input_json_delta）两侧都还原正确且有针对性测试；emit 采用同步背压（写不出去即暂停读上游），无 goroutine 泄漏面。
5. **更新链路的 `***` 哨兵闭环**：Store.UpdateEntry 对 `***` 值保留库中原值（store.go:147-154），与 system prompt 第 5 条"敏感字段不变原样回传 ***"及工具 schema 描述形成闭环，使更新草稿全程不经过明文。

## 测试盲区

- **Search 路径的隐身过滤无直接测试**：`ai_visible=false` 条目在 list/get/reveal 的隐身均有断言，但 Search 结果中隐身条目的过滤没有专门用例（现有 search 测试只建了可见条目）。
- **搜索匹配 fields JSON 的行为无测试**：非敏感字段值可搜、密文不可搜、`q` 含 `%`/`_` 作为 LIKE 通配符（等于全匹配）均未覆盖。
- **上游流中错误与截断无测试**：Anthropic 流式 error 事件（对应 P2-1）、EOF 但无 message_stop、max_tokens 截断均无模拟。
- **上游超时与重定向无测试**：client.Timeout（60s/120s）掐断长流、跟随重定向的行为未验证。
- **客户端断开传播无集成测试**：挂载层 `sendEvent` 吞掉写错误（aichat_api.go:272-277 恒返回 nil），断开检测完全依赖 r.Context() 取消传导到上游请求，该链路无测试；`TestChatStreamEmitBackpressure` 只覆盖了 emit 返回错误的路径。
- **protocol=anthropic + 空 baseURL 的默认端点行为无测试**（对应 P2-4，正因无测试才未被发现）。
- **TestConnection 遇 LLM 返回 tool_calls 的行为无测试**（对应 P3-3）。
- **history 角色不校验**：挂载层仅截取最近 40 条、不过滤 role（aichat_api.go:134-136），前端可注入 system/tool 轮影响自身会话，行为未定义也无测试。
- reveal token 无速率限制：token 泄露后可高频拖走全部敏感字段（每次都有审计但无告警/限速），该运维面在测试与设计中均未体现。
