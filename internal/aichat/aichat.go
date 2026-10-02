// Package aichat 提供 Web 内置 AI 录入助手：把用户自然语言凭据描述
// 通过 LLM（OpenAI 兼容协议 + function calling）解析为条目草稿。
// 支持先查后做：新增前查重提示、修改前定位已有条目（agent 工具循环）。
// 草稿经用户在 UI 预览确认后走现有 POST/PUT /entries 入库（校验+加密+审计复用）。
package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lpx0312/keyHive/internal/model"
)

// Config LLM 连接配置（存 settings 表；APIKey 落库前由 API 层加密）
type Config struct {
	BaseURL string `json:"base_url"` // OpenAI 兼容端点根地址，如 https://open.bigmodel.cn/api/paas/v4
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

const DefaultBaseURL = "https://open.bigmodel.cn/api/paas/v4"
const DefaultModel = "glm-4.6"

// maxToolRounds 工具循环上限（search → 再决策），防止失控
const maxToolRounds = 4

// ChatMessage 对话消息（OpenAI 格式子集）
type ChatMessage struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// ToolCallbacks 服务端注入的库查询能力；返回的条目必须已遮蔽敏感值、已过滤 ai_visible=false
type ToolCallbacks struct {
	SearchEntries func(query string) ([]model.Entry, error)
}

// Response 服务返回：回复文本 + 可选条目草稿（新建或更新）
type Response struct {
	Reply        string       `json:"reply"`
	Draft        *model.Entry `json:"draft,omitempty"`
	DraftKind    string       `json:"draft_kind,omitempty"`     // create | update
	DraftEntryID int64        `json:"draft_entry_id,omitempty"` // update 时的目标条目 id
}

const (
	DraftCreate = "create"
	DraftUpdate = "update"
)

// ---------- 工具 schema ----------

func entryFieldsSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":         map[string]any{"type": "string", "description": "字段名，英文 snake_case，如 registry/password/access_key"},
				"description": map[string]any{"type": "string", "description": "给 AI 看的中文注释：这个字段是什么、怎么用"},
				"type":        map[string]any{"type": "string", "enum": []string{"text", "url", "multiline"}},
				"is_secret":   map[string]any{"type": "boolean", "description": "密码/密钥/token 等凭据必须为 true"},
				"value":       map[string]any{"type": "string", "description": "字段值；修改已有条目且该敏感字段不变时，原样回传 ***"},
			},
			"required": []string{"key", "description", "value"},
		},
	}
}

func entryCommonSchema() map[string]any {
	return map[string]any{
		"title":       map[string]any{"type": "string", "description": "条目标题，如：华为SWR-杭州-生产"},
		"category":    map[string]any{"type": "string", "description": "category 清单中选择，无贴切项用 misc"},
		"description": map[string]any{"type": "string", "description": "条目级说明：这套凭据是干嘛的、哪个环境用"},
		"ai_visible":  map[string]any{"type": "boolean", "description": "是否对 AI 令牌可见；root/主密钥等核心凭据应设 false"},
		"fields":      entryFieldsSchema(),
	}
}

func toolsSchema() []llmTool {
	common := entryCommonSchema()
	updProps := map[string]any{
		"entry_id": map[string]any{"type": "integer", "description": "search_entries 结果中的条目 id"},
	}
	for k, v := range common {
		updProps[k] = v
	}

	return []llmTool{
		{Type: "function", Function: llmToolDef{
			Name:        "search_entries",
			Description: "查询库里已有的条目（敏感值以 *** 显示）。新增前必须先查重；用户提到修改/补充已有条目时，先用它定位目标",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "关键词，匹配标题/说明/分类；空串=列出全部"},
				},
			},
		}},
		{Type: "function", Function: llmToolDef{
			Name:        "save_entry_draft",
			Description: "把解析好的凭据信息保存为【新建】条目草稿（用户确认后才入库）。调用前应已用 search_entries 查重",
			Parameters: map[string]any{
				"type": "object", "properties": common,
				"required": []string{"title", "category", "fields"},
			},
		}},
		{Type: "function", Function: llmToolDef{
			Name:        "update_entry_draft",
			Description: "对已有条目生成【更新】草稿（用户确认后才生效）。先用 search_entries 拿到条目 id 与现有内容，改动部分由你提供；未提及的部分原样保留；敏感字段若不变，value 原样回传 ***",
			Parameters: map[string]any{
				"type": "object", "properties": updProps,
				"required": []string{"entry_id", "title", "category", "fields"},
			},
		}},
	}
}

// SystemPrompt 组装系统提示词；categories 为模板表中的可选分类清单
func SystemPrompt(categories []string) string {
	var b strings.Builder
	b.WriteString(`你是 keyHive 密钥管家的录入助手。用户用自然语言描述凭据信息，你把它落成条目草稿（用户确认后才入库）。

工作规则：
1. 以用户【最新一条】消息的意图为准，不要被对话历史带偏。
2. 新增前必须先调 search_entries 查重：若库里已有明显重复/高度相似的条目（同地址、同账号、同标题），不要直接建草稿，先告诉用户"库里已有 xxx（id=N），要更新它还是另存一条？"由用户决定。
3. 用户要求修改/补充已有条目时：先 search_entries 定位（按标题/地址等关键词），找到后基于其现有内容生成 update_entry_draft；找不到就如实说没搜到，请用户给更多信息。禁止凭记忆编造条目内容。
4. 新建条目：字段名 key 用英文 snake_case；每个字段写中文 description；密码/密钥/token 等凭据 is_secret=true；地址/用户名/组织名默认非敏感。
5. 更新条目：用户没提到的字段原样保留（从 search 结果复制）；敏感字段值不变时 value 原样回传 ***（你拿不到也不需要明文）。
6. category 从下面清单选，不贴切用 misc；ai_visible 默认 true，核心凭据（root/主密钥）设 false。
7. 关键信息（如密码）缺失时先简短追问，不要编造。
8. 回复文本用一两句中文复述你的理解/操作；密码用 *** 指代，不要完整复述。

可选 category 清单：`)
	b.WriteString(strings.Join(categories, ", "))
	return b.String()
}

// ---------- OpenAI 兼容协议 ----------

type llmRequest struct {
	Model    string       `json:"model"`
	Messages []llmMessage `json:"messages"`
	Tools    []llmTool    `json:"tools,omitempty"`
	ToolCall string       `json:"tool_choice,omitempty"`
}

type llmMessage struct {
	Role       string        `json:"role"` // system | user | assistant | tool
	Content    string        `json:"content"`
	ToolCalls  []llmToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // role=tool 时关联的调用
}

type llmTool struct {
	Type     string     `json:"type"`
	Function llmToolDef `json:"function"`
}

type llmToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type llmToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type llmResponse struct {
	Choices []llmResponseChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type llmResponseChoice struct {
	Message struct {
		Content   string        `json:"content"`
		ToolCalls []llmToolCall `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

// Chat 带 agent 工具循环的对话：LLM 可先 search_entries 再产出（新建/更新）草稿
func Chat(ctx context.Context, cfg Config, systemPrompt string, history []ChatMessage, userMessage string, cb ToolCallbacks) (*Response, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}

	msgs := []llmMessage{{Role: "system", Content: systemPrompt}}
	for _, h := range history {
		msgs = append(msgs, llmMessage{Role: h.Role, Content: h.Content})
	}
	msgs = append(msgs, llmMessage{Role: "user", Content: userMessage})

	var res Response
	for round := 0; round < maxToolRounds; round++ {
		choice, err := callLLM(ctx, cfg, msgs)
		if err != nil {
			return nil, err
		}
		if len(choice.Message.ToolCalls) == 0 {
			res.Reply = strings.TrimSpace(choice.Message.Content)
			return &res, nil
		}
		// 本轮 assistant 消息（含 tool_calls）入历史，再逐个回填 tool 结果
		msgs = append(msgs, llmMessage{Role: "assistant", Content: choice.Message.Content, ToolCalls: choice.Message.ToolCalls})
		draftDone := false
		for _, tc := range choice.Message.ToolCalls {
			content, done, err := execTool(tc, cb, &res)
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, llmMessage{Role: "tool", ToolCallID: tc.ID, Content: content})
			if done {
				draftDone = true
			}
		}
		if draftDone {
			if res.Reply == "" {
				res.Reply = strings.TrimSpace(choice.Message.Content)
			}
			if res.Reply == "" && res.Draft != nil {
				res.Reply = "已生成草稿，请确认。"
			}
			return &res, nil
		}
	}
	return nil, fmt.Errorf("工具调用超过 %d 轮仍未产出结果", maxToolRounds)
}

// execTool 执行单个工具调用；done=草稿已产出可终止循环
func execTool(tc llmToolCall, cb ToolCallbacks, res *Response) (content string, done bool, err error) {
	switch tc.Function.Name {
	case "search_entries":
		if cb.SearchEntries == nil {
			return `{"error":"查询不可用"}`, false, nil
		}
		var args struct {
			Query string `json:"query"`
		}
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		entries, err := cb.SearchEntries(args.Query)
		if err != nil {
			return `{"error":"` + err.Error() + `"}`, false, nil
		}
		if len(entries) == 0 {
			return `{"results":[]}`, false, nil
		}
		b, _ := json.Marshal(entries)
		return `{"results":` + string(b) + `}`, false, nil

	case "save_entry_draft":
		var draft model.Entry
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &draft); err != nil {
			return "", true, fmt.Errorf("草稿参数解析失败: %w", err)
		}
		if msg := draft.Validate(); msg != "" {
			return "", true, fmt.Errorf("草稿校验失败: %s", msg)
		}
		res.Draft = &draft
		res.DraftKind = DraftCreate
		return `{"ok":true}`, true, nil

	case "update_entry_draft":
		var args struct {
			EntryID     int64         `json:"entry_id"`
			Title       string        `json:"title"`
			Category    string        `json:"category"`
			Description string        `json:"description"`
			AIVisible   *bool         `json:"ai_visible"`
			Fields      []model.Field `json:"fields"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "", true, fmt.Errorf("更新草稿参数解析失败: %w", err)
		}
		if args.EntryID <= 0 {
			return "", true, fmt.Errorf("update_entry_draft 缺少 entry_id（请先 search_entries 定位）")
		}
		draft := model.Entry{
			Title:       args.Title,
			Category:    args.Category,
			Description: args.Description,
			AIVisible:   args.AIVisible == nil || *args.AIVisible,
			Fields:      args.Fields,
		}
		if msg := draft.Validate(); msg != "" {
			return "", true, fmt.Errorf("更新草稿校验失败: %s", msg)
		}
		res.Draft = &draft
		res.DraftKind = DraftUpdate
		res.DraftEntryID = args.EntryID
		return `{"ok":true}`, true, nil

	default:
		return `{"error":"unknown tool"}`, false, nil
	}
}

func callLLM(ctx context.Context, cfg Config, msgs []llmMessage) (*llmResponseChoice, error) {
	reqBody, _ := json.Marshal(llmRequest{
		Model: cfg.Model, Messages: msgs, Tools: toolsSchema(), ToolCall: "auto",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		msg := string(data)
		var e struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != nil {
			msg = e.Error.Message
		}
		return nil, fmt.Errorf("LLM 返回 HTTP %d: %s", resp.StatusCode, truncate(msg, 200))
	}
	var out llmResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("LLM 响应解析失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("LLM 返回空 choices")
	}
	return &out.Choices[0], nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
