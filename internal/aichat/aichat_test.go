package aichat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"keyhive/internal/model"
)

// mockLLMScript 按脚本依次返回响应：每轮一个 {toolCalls | content}
type mockTurn struct {
	toolName string
	toolArgs string
	content  string
}

func mockLLMScript(t *testing.T, turns []mockTurn) (*httptest.Server, *[][]llmMessage) {
	t.Helper()
	var allMsgs [][]llmMessage
	i := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llmRequest
		json.NewDecoder(r.Body).Decode(&req)
		allMsgs = append(allMsgs, req.Messages)
		if i >= len(turns) {
			json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
				"message": map[string]any{"content": "（脚本耗尽）"}, "finish_reason": "stop"}}})
			return
		}
		turn := turns[i]
		i++
		msg := map[string]any{"content": turn.content}
		if turn.toolName != "" {
			msg["tool_calls"] = []map[string]any{{
				"id": fmt.Sprintf("call-%d", i), "type": "function",
				"function": map[string]any{"name": turn.toolName, "arguments": turn.toolArgs},
			}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
			"message": msg, "finish_reason": "tool_calls"}}})
	}))
	t.Cleanup(ts.Close)
	return ts, &allMsgs
}

func draftArgs(title, category string) string {
	b, _ := json.Marshal(map[string]any{
		"title": title, "category": category, "description": "d", "ai_visible": true,
		"fields": []map[string]any{{"key": "password", "description": "密码", "is_secret": true, "value": "x"}}})
	return string(b)
}

func cbWith(entries []model.Entry) ToolCallbacks {
	return ToolCallbacks{SearchEntries: func(q string) ([]model.Entry, error) {
		var out []model.Entry
		for e := range entries {
			if q == "" || strings.Contains(entries[e].Title, q) || strings.Contains(entries[e].Category, q) {
				c := entries[e]
				for i := range c.Fields {
					if c.Fields[i].IsSecret {
						c.Fields[i].Value = "***" // 回调契约：遮蔽后给 LLM
					}
				}
				out = append(out, c)
			}
		}
		return out, nil
	}}
}

func TestCreateDraftSingleTurn(t *testing.T) {
	ts, _ := mockLLMScript(t, []mockTurn{
		{toolName: "save_entry_draft", toolArgs: draftArgs("腾讯云CCR", "docker_registry"), content: "已解析，请确认"},
	})
	resp, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "存腾讯云 CCR", cbWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Draft == nil || resp.DraftKind != DraftCreate || resp.Draft.Title != "腾讯云CCR" {
		t.Fatalf("草稿不符: %+v", resp)
	}
	if resp.Reply != "已解析，请确认" {
		t.Fatalf("回复不符: %q", resp.Reply)
	}
}

// 用户要求修改已有条目：LLM 先 search 定位 → 生成 update 草稿（敏感值回传 *** 保留原值）
func TestUpdateFlowSearchThenDraft(t *testing.T) {
	ts, msgs := mockLLMScript(t, []mockTurn{
		{toolName: "search_entries", toolArgs: `{"query":"SWR"}`},
		{toolName: "update_entry_draft", content: "已找到，将补充说明",
			toolArgs: `{"entry_id":7,"title":"华为SWR","category":"huawei_swr","description":"华为云SWR 生产环境","ai_visible":true,
				"fields":[{"key":"registry","description":"地址","value":"swr.local"},{"key":"password","description":"密码","is_secret":true,"value":"***"}]}`},
	})
	lib := []model.Entry{{
		ID: 7, Title: "华为SWR", Category: "huawei_swr", AIVisible: true,
		Fields: []model.Field{{Key: "password", Description: "密码", IsSecret: true, Value: "真实密码不外泄"}},
	}}
	resp, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil,
		"华为SWR 那条没写说明，帮我补一下", cbWith(lib))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Draft == nil || resp.DraftKind != DraftUpdate || resp.DraftEntryID != 7 {
		t.Fatalf("更新草稿不符: kind=%s id=%d", resp.DraftKind, resp.DraftEntryID)
	}
	if resp.Draft.Description != "华为云SWR 生产环境" {
		t.Fatalf("说明未更新: %q", resp.Draft.Description)
	}
	if f := resp.Draft.FieldByKey("password"); f.Value != "***" {
		t.Fatalf("敏感值应保留 *** 哨兵: %q", f.Value)
	}
	var hasToolMsg bool
	for _, m := range (*msgs)[1] {
		if m.Role == "tool" && strings.Contains(m.Content, "华为SWR") && strings.Contains(m.Content, "***") {
			hasToolMsg = true
		}
	}
	if !hasToolMsg {
		t.Fatal("search 结果未回传给 LLM（或未遮蔽）")
	}
}

// 新增查重：search 发现已有相似条目 → LLM 不建草稿、纯文本提示用户
func TestDuplicatePrompt(t *testing.T) {
	ts, _ := mockLLMScript(t, []mockTurn{
		{toolName: "search_entries", toolArgs: `{"query":"SWR"}`},
		{content: "库里已有「华为SWR」(id=7)，要更新它还是另存一条？"},
	})
	lib := []model.Entry{{ID: 7, Title: "华为SWR", Category: "huawei_swr"}}
	resp, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "存个 SWR", cbWith(lib))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Draft != nil {
		t.Fatalf("查重命中时不应出草稿: %+v", resp.Draft)
	}
	if !strings.Contains(resp.Reply, "id=7") {
		t.Fatalf("应提示已有条目: %q", resp.Reply)
	}
}

func TestSearchExecuted(t *testing.T) {
	called := false
	cb := ToolCallbacks{SearchEntries: func(q string) ([]model.Entry, error) {
		called = true
		return nil, nil
	}}
	ts, _ := mockLLMScript(t, []mockTurn{{toolName: "search_entries", toolArgs: `{"query":"x"}`}, {content: "没找到"}})
	Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "查", cb)
	if !called {
		t.Fatal("search_entries 未执行")
	}
}

func TestLoopLimit(t *testing.T) {
	ts, _ := mockLLMScript(t, []mockTurn{
		{toolName: "search_entries", toolArgs: `{}`},
		{toolName: "search_entries", toolArgs: `{}`},
		{toolName: "search_entries", toolArgs: `{}`},
		{toolName: "search_entries", toolArgs: `{}`},
		{toolName: "search_entries", toolArgs: `{}`},
	})
	_, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "x", cbWith(nil))
	if err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("应触发循环上限: %v", err)
	}
}

func TestUpdateMissingEntryID(t *testing.T) {
	ts, _ := mockLLMScript(t, []mockTurn{
		{toolName: "update_entry_draft", toolArgs: `{"entry_id":0,"title":"x","category":"misc","fields":[{"key":"a","description":"d","value":"1"}]}`},
	})
	_, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "改", cbWith(nil))
	if err == nil || !strings.Contains(err.Error(), "entry_id") {
		t.Fatalf("缺 entry_id 应报错: %v", err)
	}
}

func TestPlainReply(t *testing.T) {
	ts, _ := mockLLMScript(t, []mockTurn{{content: "密码是多少？"}})
	resp, err := Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "x", cbWith(nil))
	if err != nil || resp.Draft != nil || resp.Reply != "密码是多少？" {
		t.Fatalf("纯文本不符: %+v %v", resp, err)
	}
}

func TestHistoryCarried(t *testing.T) {
	ts, msgs := mockLLMScript(t, []mockTurn{{content: "ok"}})
	history := []ChatMessage{{Role: "user", Content: "第一轮"}, {Role: "assistant", Content: "收到"}}
	Chat(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", history, "第二轮", cbWith(nil))
	if len((*msgs)[0]) != 4 {
		t.Fatalf("历史未携带: %d 条", len((*msgs)[0]))
	}
}

func TestToolsSchemaContainsThree(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range toolsSchema() {
		names[tl.Function.Name] = true
	}
	for _, want := range []string{"search_entries", "save_entry_draft", "update_entry_draft"} {
		if !names[want] {
			t.Fatalf("缺少工具: %s", want)
		}
	}
}
