package aichat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Anthropic 协议适配（/v1/messages，x-api-key 认证）：
// 把内部 OpenAI 风格消息/工具转换为 Anthropic 格式发出，响应（含流式）还原为内部
// llmResponseChoice，Chat/ChatStream 的 agent 工具循环无需感知协议差异。

const (
	anthropicVersion   = "2023-06-01"
	anthropicMaxTokens = 4096 // Anthropic 必填参数；凭据草稿解析场景足够

	// anthropicDefaultBaseURL Anthropic 官方端点；空 baseURL 时按协议分派使用，
	// 避免 anthropic 的 x-api-key 被发往 OpenAI 默认端点
	anthropicDefaultBaseURL = "https://api.anthropic.com"
)

// anthropicURL 根地址 + 路径 → 完整端点：base 已以 /v1 结尾只补路径，否则补 /v1
// （https://api.anthropic.com → /v1/messages；https://open.bigmodel.cn/api/anthropic 同理）
func anthropicURL(base, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + path
	}
	return base + "/v1" + path
}

// ---------- 请求格式 ----------

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string            `json:"role"` // user | assistant
	Content []anthropicBlock  `json:"content"`
}

// anthropicBlock 内容块：text / tool_use / tool_result 三种形态共用一个结构（omitempty 互斥）
type anthropicBlock struct {
	Type string `json:"type"` // text | tool_use | tool_result

	Text string `json:"text,omitempty"` // type=text

	ID    string          `json:"id,omitempty"`    // type=tool_use
	Name  string          `json:"name,omitempty"`  // type=tool_use
	Input json.RawMessage `json:"input,omitempty"` // type=tool_use

	ToolUseID string `json:"tool_use_id,omitempty"` // type=tool_result
	Content   string `json:"content,omitempty"`     // type=tool_result 的结果文本
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func toAnthropicTools() []anthropicTool {
	src := toolsSchema()
	out := make([]anthropicTool, 0, len(src))
	for _, t := range src {
		out = append(out, anthropicTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: t.Function.Parameters})
	}
	return out
}

// toAnthropicMessages 内部消息转 Anthropic 格式：
// system 单独抽出；role=tool 的工具结果合并为紧跟其后的 user 轮（Anthropic 要求
// tool_result 必须出现在 user 消息里，且消息必须 user/assistant 交替）。
func toAnthropicMessages(msgs []llmMessage) (system string, out []anthropicMessage) {
	rest := msgs
	if len(rest) > 0 && rest[0].Role == "system" {
		system = rest[0].Content
		rest = rest[1:]
	}
	for _, m := range rest {
		switch m.Role {
		case "assistant":
			am := anthropicMessage{Role: "assistant"}
			if strings.TrimSpace(m.Content) != "" {
				am.Content = append(am.Content, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Function.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				am.Content = append(am.Content, anthropicBlock{
					Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args),
				})
			}
			if len(am.Content) > 0 {
				out = append(out, am)
			}
		case "user":
			out = append(out, anthropicMessage{Role: "user", Content: []anthropicBlock{{Type: "text", Text: m.Content}}})
		case "tool":
			// 连续多条工具结果并进同一条 user 消息；不与普通文本轮混合
			if n := len(out); n > 0 && out[n-1].Role == "user" && isToolResultTurn(out[n-1]) {
				out[n-1].Content = append(out[n-1].Content, anthropicBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content})
			} else {
				out = append(out, anthropicMessage{Role: "user", Content: []anthropicBlock{
					{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content},
				}})
			}
		}
	}
	return system, out
}

// isToolResultTurn 该 user 消息是否只含 tool_result 块（可继续追加工具结果）
func isToolResultTurn(m anthropicMessage) bool {
	if len(m.Content) == 0 {
		return false
	}
	for _, b := range m.Content {
		if b.Type != "tool_result" {
			return false
		}
	}
	return true
}

// ---------- 响应格式 ----------

type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"` // text | tool_use
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"` // end_turn | tool_use | ...
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (r *anthropicResponse) toChoice() *llmResponseChoice {
	var c llmResponseChoice
	var b strings.Builder
	for _, blk := range r.Content {
		switch blk.Type {
		case "text":
			b.WriteString(blk.Text)
		case "tool_use":
			args := string(blk.Input)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			tc := llmToolCall{ID: blk.ID, Type: "function"}
			tc.Function.Name = blk.Name
			tc.Function.Arguments = args
			c.Message.ToolCalls = append(c.Message.ToolCalls, tc)
		}
	}
	c.Message.Content = b.String()
	c.FinishReason = "stop"
	if r.StopReason == "tool_use" {
		c.FinishReason = "tool_calls"
	}
	return &c
}

func anthropicErrMsg(data []byte) string {
	var e anthropicResponse
	if json.Unmarshal(data, &e) == nil && e.Error != nil {
		return e.Error.Message
	}
	return string(data)
}

// callAnthropic 统一入口：emit=nil 走非流式，否则走流式并逐段转发回复文本
func callAnthropic(ctx context.Context, cfg Config, msgs []llmMessage, emit func(StreamEvent) error) (*llmResponseChoice, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = anthropicDefaultBaseURL
	}
	system, amsgs := toAnthropicMessages(msgs)
	reqBody, _ := json.Marshal(anthropicRequest{
		Model:     cfg.Model,
		MaxTokens: anthropicMaxTokens,
		System:    system,
		Messages:  amsgs,
		Tools:     toAnthropicTools(),
		Stream:    emit != nil,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		anthropicURL(cfg.BaseURL, "/messages"), bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	if emit != nil {
		req.Header.Set("Accept", "text/event-stream")
	}

	timeout := 60 * time.Second
	if emit != nil {
		timeout = 120 * time.Second
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("LLM 返回 HTTP %d: %s", resp.StatusCode, truncate(anthropicErrMsg(data), 200))
	}
	if emit != nil {
		return parseAnthropicStream(resp.Body, emit)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	var out anthropicResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("LLM 响应解析失败: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("LLM 错误: %s", out.Error.Message)
	}
	if len(out.Content) == 0 {
		return nil, fmt.Errorf("LLM 返回空 content")
	}
	return out.toChoice(), nil
}

// parseAnthropicStream 解析 Anthropic SSE：text_delta 转发回复文本，
// tool_use 块按 index 分片累积（input_json_delta），流结束还原完整 choice
func parseAnthropicStream(body io.Reader, emit func(StreamEvent) error) (*llmResponseChoice, error) {
	var contentB strings.Builder
	tcs := map[int]*toolCallAcc{}

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"` // text_delta | input_json_delta
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			Error *struct {
				Type    string `json:"type"`    // overloaded_error | rate_limit_error | ...
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "error":
			// 协议规定流中途失败（限流/credit 不足/overload 等）会下发 error 事件，
			// 必须作为错误返回，否则表现为"空/半截回复 + 成功"
			msg := "未知错误"
			if ev.Error != nil {
				msg = strings.TrimSpace(ev.Error.Message)
				if ev.Error.Type != "" {
					msg = ev.Error.Type + ": " + msg
				}
			}
			return nil, fmt.Errorf("LLM 流式错误: %s", msg)
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				acc := tcs[ev.Index]
				if acc == nil {
					acc = &toolCallAcc{}
					tcs[ev.Index] = acc
				}
				acc.id = ev.ContentBlock.ID
				acc.name = ev.ContentBlock.Name
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					contentB.WriteString(ev.Delta.Text)
					if err := emit(StreamEvent{Type: "reply_delta", Text: ev.Delta.Text}); err != nil {
						return nil, err
					}
				}
			case "input_json_delta":
				acc := tcs[ev.Index]
				if acc == nil {
					acc = &toolCallAcc{}
					tcs[ev.Index] = acc
				}
				acc.argsParts = append(acc.argsParts, ev.Delta.PartialJSON)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取流失败: %w", err)
	}

	var out llmResponseChoice
	out.Message.Content = contentB.String()
	keys := make([]int, 0, len(tcs))
	for k := range tcs {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, i := range keys {
		acc := tcs[i]
		if acc == nil {
			continue
		}
		tc := llmToolCall{ID: acc.id, Type: "function"}
		tc.Function.Name = acc.name
		args := strings.Join(acc.argsParts, "")
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		tc.Function.Arguments = args
		out.Message.ToolCalls = append(out.Message.ToolCalls, tc)
	}
	out.FinishReason = "stop"
	if len(out.Message.ToolCalls) > 0 {
		out.FinishReason = "tool_calls"
	}
	return &out, nil
}
