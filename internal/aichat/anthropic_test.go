package aichat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockAnthropicScript 按脚本依次返回 Anthropic /v1/messages 响应，记录每轮请求消息
func mockAnthropicScript(t *testing.T, turns [][]map[string]any) (*httptest.Server, *[][]anthropicMessage) {
	t.Helper()
	var (
		allMsgs [][]anthropicMessage
		mu      sync.Mutex
		i       int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthropicRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		allMsgs = append(allMsgs, req.Messages)
		idx := i
		i++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if idx >= len(turns) {
			json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "（脚本耗尽）"}}, "stop_reason": "end_turn"})
			return
		}
		stop := "end_turn"
		if len(turns[idx]) > 0 {
			stop = "tool_use"
		}
		json.NewEncoder(w).Encode(map[string]any{"content": turns[idx], "stop_reason": stop})
	}))
	t.Cleanup(ts.Close)
	return ts, &allMsgs
}

func textBlock(s string) map[string]any   { return map[string]any{"type": "text", "text": s} }
func toolUseBlock(name, args string) map[string]any {
	return map[string]any{"type": "tool_use", "id": "call-" + name, "name": name, "input": json.RawMessage(args)}
}

// 请求形状 + 单轮建草稿：URL /v1/messages、x-api-key 认证、system 抽出、tools 带 input_schema
func TestAnthropicRequestShapeAndDraft(t *testing.T) {
	var (
		gotPath, gotKey, gotVer string
		gotReq                 anthropicRequest
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVer = r.Header.Get("anthropic-version")
		json.NewDecoder(r.Body).Decode(&gotReq)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				textBlock("已解析，请确认"),
				toolUseBlock("save_entry_draft", draftArgs("腾讯云CCR", "docker_registry")),
			},
			"stop_reason": "tool_use",
		})
	}))
	t.Cleanup(ts.Close)

	resp, err := Chat(context.Background(), Config{
		Protocol: ProtocolAnthropic, BaseURL: ts.URL, APIKey: "k", Model: "claude-sonnet-4-5",
	}, "你是系统提示", nil, "存腾讯云 CCR", cbWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("路径不符: %s", gotPath)
	}
	if gotKey != "k" || gotVer != "2023-06-01" {
		t.Fatalf("认证头不符: key=%s ver=%s", gotKey, gotVer)
	}
	if gotReq.System != "你是系统提示" {
		t.Fatalf("system 未抽出: %q", gotReq.System)
	}
	if gotReq.MaxTokens <= 0 {
		t.Fatalf("max_tokens 必填: %d", gotReq.MaxTokens)
	}
	if len(gotReq.Tools) != 3 || gotReq.Tools[0].Name != "search_entries" || gotReq.Tools[0].InputSchema == nil {
		t.Fatalf("tools/input_schema 不符: %+v", gotReq.Tools)
	}
	if len(gotReq.Messages) == 0 || gotReq.Messages[0].Role != "user" {
		t.Fatalf("首条消息应为 user: %+v", gotReq.Messages)
	}
	if resp.Draft == nil || resp.Draft.Title != "腾讯云CCR" || resp.DraftKind != DraftCreate {
		t.Fatalf("草稿不符: %+v", resp)
	}
	if resp.Reply != "已解析，请确认" {
		t.Fatalf("回复不符: %q", resp.Reply)
	}
}

// search → update 两轮工具循环：tool_result 回填为 user 轮、tool_use_id 关联
func TestAnthropicSearchThenUpdate(t *testing.T) {
	ts, msgs := mockAnthropicScript(t, [][]map[string]any{
		{toolUseBlock("search_entries", `{"query":"SWR"}`)},
		{textBlock("已找到"), toolUseBlock("update_entry_draft",
			`{"entry_id":7,"title":"华为SWR","category":"huawei_swr","description":"d","ai_visible":true,`+
				`"fields":[{"key":"password","description":"密码","is_secret":true,"value":"***"}]}`)},
	})
	resp, err := Chat(context.Background(), Config{
		Protocol: ProtocolAnthropic, BaseURL: ts.URL, APIKey: "k", Model: "claude-sonnet-4-5",
	}, "sys", nil, "补充 SWR 说明", cbWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Draft == nil || resp.DraftKind != DraftUpdate || resp.DraftEntryID != 7 {
		t.Fatalf("更新草稿不符: %+v", resp)
	}

	all := *msgs
	if len(all) != 2 {
		t.Fatalf("应有两轮请求，实际 %d", len(all))
	}
	m2 := all[1]
	if len(m2) != 3 {
		t.Fatalf("第二轮消息数应 3，实际 %d: %+v", len(m2), m2)
	}
	// 第一轮 assistant 无文本，只含 search_entries 的 tool_use 块
	if m2[1].Role != "assistant" || len(m2[1].Content) != 1 || m2[1].Content[0].Type != "tool_use" {
		t.Fatalf("assistant tool_use 轮不符: %+v", m2[1])
	}
	if m2[2].Role != "user" || len(m2[2].Content) != 1 || m2[2].Content[0].Type != "tool_result" {
		t.Fatalf("tool_result 轮不符: %+v", m2[2])
	}
	if m2[2].Content[0].ToolUseID != m2[1].Content[0].ID {
		t.Fatalf("tool_use_id 不关联: %s vs %s", m2[2].Content[0].ToolUseID, m2[1].Content[0].ID)
	}
	if !strings.Contains(m2[2].Content[0].Content, "results") {
		t.Fatalf("tool_result 内容应含查询结果: %s", m2[2].Content[0].Content)
	}
}

// 流式：text_delta 逐段转发 + input_json_delta 分片还原 tool_use
func TestAnthropicStreamFlow(t *testing.T) {
	var deltas []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		seq := []string{
			`{"type":"message_start","message":{}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"已解析"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"，请确认"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-9","name":"save_entry_draft"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"title\":\"ACR\",\"categ"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"ory\":\"misc\",\"description\":\"d\",\"fields\":[{\"key\":\"password\",\"descr"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"iption\":\"密码\",\"is_secret\":true,\"value\":\"x\"}]}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
			`{"type":"message_stop"}`,
		}
		for _, s := range seq {
			fmt.Fprintf(w, "data: %s\n\n", s)
			fl.Flush()
		}
	}))
	t.Cleanup(ts.Close)

	resp, err := ChatStream(context.Background(), Config{
		Protocol: ProtocolAnthropic, BaseURL: ts.URL, APIKey: "k", Model: "claude-sonnet-4-5",
	}, "sys", nil, "存 ACR", cbWith(nil), func(ev StreamEvent) error {
		if ev.Type == "reply_delta" {
			deltas = append(deltas, ev.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != "已解析，请确认" {
		t.Fatalf("流式文本不符: %q", deltas)
	}
	if resp.Draft == nil || resp.Draft.Title != "ACR" || len(resp.Draft.Fields) != 1 || resp.Draft.Fields[0].Value != "x" {
		t.Fatalf("分片还原的草稿不符: %+v", resp.Draft)
	}
}

func TestAnthropicURL(t *testing.T) {
	cases := [][3]string{
		{"https://api.anthropic.com", "/messages", "https://api.anthropic.com/v1/messages"},
		{"https://open.bigmodel.cn/api/anthropic/", "/messages", "https://open.bigmodel.cn/api/anthropic/v1/messages"},
		{"https://x.example/v1/", "/models", "https://x.example/v1/models"},
	}
	for _, c := range cases {
		if got := anthropicURL(c[0], c[1]); got != c[2] {
			t.Fatalf("anthropicURL(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
