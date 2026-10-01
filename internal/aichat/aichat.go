// Package aichat 提供 Web 内置 AI 录入助手：把用户自然语言凭据描述
// 通过 LLM（OpenAI 兼容协议 + function calling）解析为条目草稿。
// 草稿经用户在 UI 预览确认后走现有 POST /entries 入库（校验+加密+审计复用）。
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

	"keyhive/internal/model"
)

// Config LLM 连接配置（存 settings 表；APIKey 落库前由 API 层加密）
type Config struct {
	BaseURL string `json:"base_url"` // OpenAI 兼容端点根地址，如 https://open.bigmodel.cn/api/paas/v4
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

const DefaultBaseURL = "https://open.bigmodel.cn/api/paas/v4"
const DefaultModel = "glm-4.6"

// ChatMessage 对话消息（OpenAI 格式子集）
type ChatMessage struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// Response 服务返回：回复文本 + 可选条目草稿
type Response struct {
	Reply string       `json:"reply"`
	Draft *model.Entry `json:"draft,omitempty"`
}

// draftToolArgs save_entry_draft 的 JSON Schema（与 model.Entry 字段对齐）
func draftToolArgs() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":       map[string]any{"type": "string", "description": "条目标题，如：华为SWR-杭州-生产"},
			"category":    map[string]any{"type": "string", "description": "从系统提示的 category 清单中选择，无贴切项用 misc"},
			"description": map[string]any{"type": "string", "description": "条目级说明：这套凭据是干嘛的、哪个环境用"},
			"ai_visible":  map[string]any{"type": "boolean", "description": "是否对 AI 令牌可见；root/主密钥等核心凭据应设 false"},
			"fields": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key":         map[string]any{"type": "string", "description": "字段名，英文 snake_case，如 registry/password/access_key"},
						"description": map[string]any{"type": "string", "description": "给 AI 看的中文注释：这个字段是什么、怎么用"},
						"type":        map[string]any{"type": "string", "enum": []string{"text", "url", "multiline"}},
						"is_secret":   map[string]any{"type": "boolean", "description": "密码/密钥/token 等凭据必须为 true"},
						"value":       map[string]any{"type": "string", "description": "字段值"},
					},
					"required": []string{"key", "description", "value"},
				},
			},
		},
		"required": []string{"title", "category", "fields"},
	}
}

// SystemPrompt 组装系统提示词；categories 为模板表中的可选分类清单
func SystemPrompt(categories []string) string {
	var b strings.Builder
	b.WriteString(`你是 keyHive 密钥管家的录入助手。用户会用自然语言描述一套凭据/账号信息，你的任务是把它解析成结构化的条目草稿。

规则：
1. 信息完整时，调用 save_entry_draft 工具输出草稿，并用一两句中文向用户复述你的理解（密码用 *** 指代，不要在回复文本里完整复述）。
2. 字段名 key 用英文 snake_case（如 registry_url、access_key_id、password、org）。
3. 每个字段必须写中文 description 注释（说明这是什么、怎么用）。
4. 密码/密钥/token/私钥等凭据字段必须 is_secret=true；地址、用户名、组织名、端口等默认非敏感。
5. category 优先从下面的清单选；都不贴切时用 misc。
6. ai_visible 默认 true；用户说明是核心凭据（root 密码、主密钥、银行级）时设 false。
7. 关键信息缺失（如只有用户名没密码）时先简短追问，不要编造值。

可选 category 清单：`)
	b.WriteString(strings.Join(categories, ", "))
	return b.String()
}

// llmRequest / llmResponse OpenAI 兼容协议的最小子集
type llmRequest struct {
	Model    string       `json:"model"`
	Messages []llmMessage `json:"messages"`
	Tools    []llmTool    `json:"tools,omitempty"`
	ToolCall string       `json:"tool_choice,omitempty"`
}

type llmMessage struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []llmToolCall `json:"tool_calls,omitempty"`
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
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []llmToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat 调 LLM 解析对话，返回回复与草稿（history 为之前的 user/assistant 轮次）
func Chat(ctx context.Context, cfg Config, systemPrompt string, history []ChatMessage, userMessage string) (*Response, error) {
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

	reqBody, _ := json.Marshal(llmRequest{
		Model:    cfg.Model,
		Messages: msgs,
		Tools: []llmTool{{
			Type: "function",
			Function: llmToolDef{
				Name:        "save_entry_draft",
				Description: "把解析好的凭据信息保存为条目草稿（用户确认后才入库）",
				Parameters:  draftToolArgs(),
			},
		}},
		ToolCall: "auto",
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
	res := &Response{Reply: strings.TrimSpace(out.Choices[0].Message.Content)}
	for _, tc := range out.Choices[0].Message.ToolCalls {
		if tc.Function.Name != "save_entry_draft" {
			continue
		}
		var draft model.Entry
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &draft); err != nil {
			return nil, fmt.Errorf("草稿参数解析失败: %w", err)
		}
		if msg := draft.Validate(); msg != "" {
			return nil, fmt.Errorf("草稿校验失败: %s", msg)
		}
		res.Draft = &draft
		break
	}
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestResult 连接测试结果
type TestResult struct {
	OK        bool   `json:"ok"`
	Model     string `json:"model"`
	Reply     string `json:"reply,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// TestConnection 用最小请求验证 LLM 配置连通性（不消耗 tools，max_tokens 限制开销）
func TestConnection(ctx context.Context, cfg Config) *TestResult {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	res := &TestResult{Model: cfg.Model}

	reqBody, _ := json.Marshal(map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": "ping，请只回复 pong"},
		},
		"max_tokens": 16,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	start := time.Now()
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = fmt.Sprintf("连接失败: %v", err)
		return res
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		res.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
		return res
	}
	var out llmResponse
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		res.Error = "响应格式异常（非 OpenAI 兼容？）"
		return res
	}
	res.OK = true
	res.Reply = truncate(strings.TrimSpace(out.Choices[0].Message.Content), 60)
	return res
}
