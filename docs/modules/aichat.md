# aichat 模块（internal/aichat）

## 职责

Web 内置 AI 录入助手的协议层：把用户自然语言凭据描述经 LLM（agent 工具循环）解析为条目草稿。支持 OpenAI 兼容（`/chat/completions`，Bearer）与 Anthropic（`/v1/messages`，x-api-key）双协议、非流式与 SSE 流式两种形态、服务商模型清单拉取、连通性测试与 15 个内置服务商预设。本包不落库：草稿由用户在 UI 确认后走 `POST/PUT /entries`，复用既有校验+加密+审计链路。

## 关键文件与入口

| 文件 | 内容 |
|---|---|
| `internal/aichat/aichat.go` | `Config`、`Chat`、工具 schema（search_entries / save_entry_draft / update_entry_draft）、`SystemPrompt`、`execTool`、OpenAI 非流式 `callLLM` |
| `internal/aichat/anthropic.go` | Anthropic 适配：`toAnthropicMessages` 消息/工具互转、`callAnthropic`（emit=nil 非流式）、`parseAnthropicStream` SSE 解析、`anthropicURL` |
| `internal/aichat/stream.go` | `StreamEvent`、`ChatStream`、OpenAI SSE 解析 `callLLMStream`、tool_calls 按 index 分片累积（`toolCallAcc`） |
| `internal/aichat/models.go` | `ListModels`：OpenAI `/models`（带 /v1 回退）与 Anthropic `/v1/models` 分页（`fetchModelsOnce`） |
| `internal/aichat/testconn.go` | `TestConnection` 连通性测试（实测往返耗时） |
| `internal/aichat/presets.go` | `Preset` 与 `Presets`（15 个服务商预设：12 个 OpenAI 兼容 + 3 个 Anthropic 兼容） |

## 对外接口

- `Config{Protocol, BaseURL, APIKey, Model}`：
  - `ProtocolOrDefault()`：空串/未知值归一为 `ProtocolOpenAI`（老库无协议配置时的行为）；
  - 常量 `ProtocolOpenAI` / `ProtocolAnthropic`；`DefaultBaseURL = "https://open.bigmodel.cn/api/paas/v4"`；`DefaultModel = "glm-4.6"`；
  - **默认端点按协议分派**：`Config.defaultBaseURL()`——openai → `DefaultBaseURL`，anthropic → `anthropicDefaultBaseURL`（`https://api.anthropic.com`）；`Chat` / `ChatStream` / `ListModels` / `TestConnection` 四个入口一致生效，且必须在协议分派前填充，否则 anthropic 的 x-api-key 会被发往 OpenAI 端点。
- 对话入口：
  - `Chat(ctx, cfg, systemPrompt, history []ChatMessage, userMessage, cb ToolCallbacks) (*Response, error)`；
  - `ChatStream(...同参..., emit func(StreamEvent) error) (*Response, error)`：回复逐字经 `reply_delta` 发出，agent 每步经 `status` 发出，emit 阻塞即背压；最终结果同时整体返回；
  - 工具循环上限 `maxToolRounds = 4`（search → 再决策，防失控），超限报「工具调用超过 4 轮仍未产出结果」。
- 数据类型：
  - `ToolCallbacks{SearchEntries func(query) ([]model.Entry, error)}`——服务端注入的库查询回调，契约：返回条目必须已遮蔽敏感值、已过滤 `ai_visible=false`；
  - `Response{Reply, Draft *model.Entry, DraftKind, DraftEntryID}`；`DraftCreate = "create"` / `DraftUpdate = "update"`；
  - `StreamEvent{Type: status|reply_delta|draft|error|done, Text, Draft, DraftKind, DraftEntryID}`；
  - `SystemPrompt(categories []string) string`：8 条工作纪律（最新消息优先、新增先查重、更新先定位、snake_case 字段名、中文 description、敏感不变回传 `***`、缺关键信息先追问、回复不复述密码）+ category 清单。
- 辅助：`ListModels(ctx, cfg) ([]string, error)`、`TestConnection(ctx, cfg) map[string]any`（ok/model/protocol/latency_ms/reply，发一条最小消息带工具 schema 实测）、`Presets`。

三个 LLM 工具（`toolsSchema()`，两协议共用同一 schema）：

| 工具 | 用途 | 终止循环 |
|---|---|---|
| `search_entries` | 查库（遮蔽结果）；新增前必须查重、更新前先定位 | 否 |
| `save_entry_draft` | 产出新建草稿（title/category/fields 必填） | 是 |
| `update_entry_draft` | 产出更新草稿（额外必填 entry_id） | 是 |

## 依赖关系

- 依赖：`internal/model`（Entry/Field 与 `Validate`）。
- 被依赖：`internal/api`（/ai-chat、/ai-chat/stream、/ai-config/test、/ai-config/models 四个端点）。
- 不直接访问 DB 与密钥：
  - APIKey 由 API 层从 settings 表解密后注入 Config；
  - 搜索回调由 API 层闭包实现（`store.ListEntries` + `AIVisible` 过滤 + `MaskSecrets`）；
  - 草稿入库由前端确认后走 `POST/PUT /entries`，本包零落库。

## 安全与不变量

1. **Anthropic 流 error 事件必须传播为错误**：`parseAnthropicStream` 收到 `type:"error"`（限流/credit 不足/overload 等）立即返回 `LLM 流式错误: <type>: <message>`，绝不表现为“空/半截回复 + 成功”（`TestBug_AnthropicStreamErrorPropagated`）；OpenAI 路径非 200 同样透传（`TestStreamUpstream5xxPropagates`）。
2. **搜索回调的遮蔽契约**：`ToolCallbacks` 注释即契约——LLM 只能拿到遮蔽值；更新草稿对不变的敏感字段回传 `***`（系统提示词第 5 条与之呼应），明文永不出现在模型上下文。
3. **草稿不落库**：`save_entry_draft` / `update_entry_draft` 只填 `res.Draft` 并过 `Validate`，入库由前端确认后走既有端点；`update_entry_draft` 缺 `entry_id` 直接报错（必须先 search 定位）；未知工具名返回 `{"error":"unknown tool"}`。
4. **协议转换正确性**：Anthropic 要求 tool_result 必须出现在 user 消息里且 user/assistant 交替——`toAnthropicMessages` 把连续多条工具结果并入同一条 user 轮（`isToolResultTurn` 判定）；流式 tool_use 参数按 `input_json_delta` 分片累积、按 index 排序还原；`max_tokens=4096` 必填。
5. 上游错误信息按 rune 截断（`truncate`，byte 截断会把中文切成乱码）；非流式响应 `LimitReader` 限读（Anthropic 8MB、模型列表 1MB）；HTTP 超时非流式 60s、流式 120s。

## 测试锚点

`TestBug_AnthropicStreamErrorPropagated`、`TestBug_AnthropicEmptyBaseURLUsesAnthropicEndpoint`（默认端点分派）、`TestExplicitBaseURLUntouched`、`TestAnthropicRequestShapeAndDraft`、`TestAnthropicSearchThenUpdate`、`TestChatStreamAgentFlowWithFragmentedToolArgs`、`TestChatStreamEmitBackpressure`、`TestLoopLimit` / `TestChatToolLoopLimit`、`TestUpdateMissingEntryID`、`TestListModelsOpenAIFallbackV1`、`TestListModelsAnthropicPaginated`、`TestTestConnectionAnthropic`、`TestPresetsValid`。
