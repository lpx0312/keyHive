package aichat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockLLM 起一个返回固定 tool_calls 的假 LLM 服务，并记录收到的请求
func mockLLM(t *testing.T, toolArgs map[string]any, replyText string) (*httptest.Server, *llmRequest) {
	t.Helper()
	var captured llmRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("鉴权头错误: %s", auth)
		}
		json.NewDecoder(r.Body).Decode(&captured)
		resp := map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": replyText,
					"tool_calls": []map[string]any{{
						"id":   "1",
						"type": "function",
						"function": map[string]any{
							"name":      "save_entry_draft",
							"arguments": mustJSON(toolArgs),
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(ts.Close)
	return ts, &captured
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestChatParsesDraft(t *testing.T) {
	toolArgs := map[string]any{
		"title":       "华为SWR-测试",
		"category":    "huawei_swr",
		"description": "测试解析",
		"ai_visible":  true,
		"fields": []map[string]any{
			{"key": "registry", "description": "仓库地址", "type": "text", "is_secret": false, "value": "swr.local"},
			{"key": "password", "description": "登录密码", "type": "text", "is_secret": true, "value": "p@ss"},
		},
	}
	ts, captured := mockLLM(t, toolArgs, "我理解了，请确认")

	resp, err := Chat(context.Background(),
		Config{BaseURL: ts.URL, APIKey: "test-key", Model: "glm-test"},
		SystemPrompt([]string{"huawei_swr", "mysql"}), nil, "我有个华为云仓库")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reply != "我理解了，请确认" {
		t.Fatalf("回复不符: %q", resp.Reply)
	}
	if resp.Draft == nil {
		t.Fatal("未解析出草稿")
	}
	if resp.Draft.Title != "华为SWR-测试" || len(resp.Draft.Fields) != 2 {
		t.Fatalf("草稿不符: %+v", resp.Draft)
	}
	if f := resp.Draft.FieldByKey("password"); f == nil || !f.IsSecret || f.Value != "p@ss" {
		t.Fatalf("敏感字段解析错误: %+v", f)
	}
	// 请求侧：system prompt 带 category 清单 + tools 定义 + 用户消息
	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "save_entry_draft" {
		t.Fatalf("tools 定义缺失: %+v", captured.Tools)
	}
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" ||
		!strings.Contains(captured.Messages[0].Content, "huawei_swr") {
		t.Fatalf("消息组装错误: %+v", captured.Messages)
	}
	if captured.Messages[1].Content != "我有个华为云仓库" {
		t.Fatal("用户消息未传递")
	}
}

func TestChatInvalidDraftRejected(t *testing.T) {
	// 空标题 → 草稿校验失败
	ts, _ := mockLLM(t, map[string]any{
		"title": "  ", "category": "misc",
		"fields": []map[string]any{{"key": "a", "description": "d", "value": "1"}},
	}, "ok")
	_, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "test-key"},
		"sys", nil, "x")
	if err == nil || !strings.Contains(err.Error(), "标题不能为空") {
		t.Fatalf("空标题应校验失败: %v", err)
	}
}

func TestChatPlainReplyWithoutTool(t *testing.T) {
	// LLM 只回文本（信息不全追问场景）：无草稿，不报错
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": "密码是多少？"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer ts.Close()
	resp, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "x")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Draft != nil || resp.Reply != "密码是多少？" {
		t.Fatalf("纯文本回复解析错误: %+v", resp)
	}
}

func TestChatLLMError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer ts.Close()
	_, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "bad"}, "sys", nil, "x")
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("应透出 LLM 错误: %v", err)
	}
}

func TestHistoryCarried(t *testing.T) {
	var captured llmRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer ts.Close()
	history := []ChatMessage{{Role: "user", Content: "第一轮"}, {Role: "assistant", Content: "收到"}}
	Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", history, "第二轮")
	if len(captured.Messages) != 4 { // system + 2 history + 1 new
		t.Fatalf("历史未携带: %d 条", len(captured.Messages))
	}
}
