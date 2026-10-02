package aichat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lpx0312/keyHive/internal/model"
)

// StreamEvent SSE 事件：前端按 type 渐进更新 UI
type StreamEvent struct {
	Type         string       `json:"type"` // status | reply_delta | draft | error | done
	Text         string       `json:"text,omitempty"`
	Draft        *model.Entry `json:"draft,omitempty"`
	DraftKind    string       `json:"draft_kind,omitempty"`
	DraftEntryID int64        `json:"draft_entry_id,omitempty"`
}

// ChatStream 流式版 Chat：LLM 逐字回复经 reply_delta 发出，agent 每步经 status 发出。
// emit 阻塞即背压（SSE 写不出去时暂停读取）。最终结果同时整体返回。
func ChatStream(ctx context.Context, cfg Config, systemPrompt string, history []ChatMessage, userMessage string, cb ToolCallbacks, emit func(StreamEvent) error) (*Response, error) {
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
		choice, err := callLLMStream(ctx, cfg, msgs, emit)
		if err != nil {
			return nil, err
		}
		if len(choice.Message.ToolCalls) == 0 {
			res.Reply = choice.Message.Content
			return &res, nil
		}
		msgs = append(msgs, llmMessage{Role: "assistant", Content: choice.Message.Content, ToolCalls: choice.Message.ToolCalls})
		draftDone := false
		for _, tc := range choice.Message.ToolCalls {
			switch tc.Function.Name {
			case "search_entries":
				emitSafe(emit, StreamEvent{Type: "status", Text: "🔍 正在查询库中…"})
			case "save_entry_draft":
				emitSafe(emit, StreamEvent{Type: "status", Text: "📝 正在生成草稿…"})
			case "update_entry_draft":
				emitSafe(emit, StreamEvent{Type: "status", Text: "📝 正在生成更新草稿…"})
			}
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
				res.Reply = choice.Message.Content
			}
			return &res, nil
		}
	}
	return nil, fmt.Errorf("工具调用超过 %d 轮仍未产出结果", maxToolRounds)
}

func emitSafe(emit func(StreamEvent) error, ev StreamEvent) {
	if emit != nil {
		_ = emit(ev)
	}
}

// 流式 delta 的 tool_calls 按 index 分片累积
type toolCallAcc struct {
	id        string
	name      string
	argsParts []string
}

// callLLMStream 发起 stream:true 请求，逐 delta 转发回复文本，流结束返回完整 choice
func callLLMStream(ctx context.Context, cfg Config, msgs []llmMessage, emit func(StreamEvent) error) (*llmResponseChoice, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"model":    cfg.Model,
		"messages": msgs,
		"tools":    toolsSchema(),
		"tool_choice": "auto",
		"stream":   true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data := make([]byte, 4096)
		n, _ := resp.Body.Read(data)
		return nil, fmt.Errorf("LLM 返回 HTTP %d: %s", resp.StatusCode, truncate(string(data[:n]), 200))
	}

	var (
		contentB strings.Builder
		mu       sync.Mutex
		tcs      = map[int]*toolCallAcc{}
	)
	sc := bufio.NewScanner(resp.Body)
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
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			contentB.WriteString(d.Content)
			if emit != nil {
				if err := emit(StreamEvent{Type: "reply_delta", Text: d.Content}); err != nil {
					return nil, err
				}
			}
		}
		for _, tc := range d.ToolCalls {
			mu.Lock()
			acc := tcs[tc.Index]
			if acc == nil {
				acc = &toolCallAcc{}
				tcs[tc.Index] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.argsParts = append(acc.argsParts, tc.Function.Arguments)
			mu.Unlock()
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取流失败: %w", err)
	}

	var out llmResponseChoice
	out.Message.Content = contentB.String()
	// tool_calls 按流内 index 顺序还原
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
		var tc llmToolCall
		tc.ID = acc.id
		tc.Type = "function"
		tc.Function.Name = acc.name
		tc.Function.Arguments = strings.Join(acc.argsParts, "")
		out.Message.ToolCalls = append(out.Message.ToolCalls, tc)
	}
	return &out, nil
}
