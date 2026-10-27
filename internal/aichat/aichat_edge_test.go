package aichat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lpx0312/keyHive/internal/model"
)

func TestSystemPrompt(t *testing.T) {
	p := SystemPrompt([]string{"docker_registry", "ssh_server"})
	if !strings.Contains(p, "docker_registry, ssh_server") || !strings.Contains(p, "录入助手") {
		t.Fatalf("SystemPrompt 内容异常: %.80s", p)
	}
	if !strings.Contains(SystemPrompt(nil), "可选 category 清单：") {
		t.Fatal("空清单也应可用")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Fatal("短串应原样")
	}
	got := truncate("很长的中文内容要被截断了", 5)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("截断应带省略号: %q", got)
	}
}

// 工具调用的错误/边界分支（同包直调 execTool）
func TestExecToolBranches(t *testing.T) {
	var res Response
	cb := ToolCallbacks{}

	// search：无 callback
	var done bool
	c, done, err := execTool(llmToolCall{Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: "search_entries", Arguments: `{"query":"x"}`}}, cb, &res)
	if err != nil || done || c != `{"error":"查询不可用"}` {
		t.Fatalf("无 callback: %q %v %v", c, done, err)
	}
	// search：callback 报错
	cb.SearchEntries = func(q string) ([]model.Entry, error) { return nil, context.Canceled }
	c, _, _ = execTool(mkToolCall("search_entries", `{"query":"x"}`), cb, &res)
	if !strings.Contains(c, "error") {
		t.Fatalf("callback 错误应回 error json: %q", c)
	}
	// search：空结果
	cb.SearchEntries = func(q string) ([]model.Entry, error) { return nil, nil }
	c, _, _ = execTool(mkToolCall("search_entries", `{"query":"x"}`), cb, &res)
	if c != `{"results":[]}` {
		t.Fatalf("空结果应回空数组: %q", c)
	}
	// search：坏参数 JSON（query 缺省为空）
	cb.SearchEntries = func(q string) ([]model.Entry, error) {
		if q != "" {
			t.Fatalf("坏 JSON 时 query 应为空: %q", q)
		}
		return nil, nil
	}
	execTool(mkToolCall("search_entries", `not-json`), cb, &res)

	// save_entry_draft：坏 JSON
	if _, _, err := execTool(mkToolCall("save_entry_draft", `{bad`), cb, &res); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
	// save_entry_draft：校验失败（空标题）
	if _, _, err := execTool(mkToolCall("save_entry_draft", `{"title":"","fields":[]}`), cb, &res); err == nil {
		t.Fatal("校验失败应报错")
	}
	// save_entry_draft：成功
	_, done, err = execTool(mkToolCall("save_entry_draft", `{"title":"T","category":"misc","fields":[{"key":"k","description":"d","value":"v"}]}`), cb, &res)
	if err != nil || !done || res.Draft == nil || res.DraftKind != DraftCreate {
		t.Fatalf("草稿创建失败: %v %+v", err, res)
	}
	// update_entry_draft：坏 JSON / entry_id 缺失 / 校验失败 / 成功
	if _, _, err := execTool(mkToolCall("update_entry_draft", `{bad`), cb, &res); err == nil {
		t.Fatal("update 坏 JSON 应报错")
	}
	if _, _, err := execTool(mkToolCall("update_entry_draft", `{"entry_id":0,"title":"t","category":"c","fields":[]}`), cb, &res); err == nil {
		t.Fatal("entry_id 缺失应报错")
	}
	if _, _, err := execTool(mkToolCall("update_entry_draft", `{"entry_id":5,"title":"","fields":[]}`), cb, &res); err == nil {
		t.Fatal("update 校验失败应报错")
	}
	res2 := Response{}
	_, done, err = execTool(mkToolCall("update_entry_draft", `{"entry_id":5,"title":"T2","category":"c","ai_visible":false,"fields":[{"key":"k","description":"d","value":"v"}]}`), cb, &res2)
	if err != nil || !done || res2.DraftKind != DraftUpdate || res2.DraftEntryID != 5 {
		t.Fatalf("update 草稿失败: %v %+v", err, res2)
	}
	if res2.Draft.AIVisible {
		t.Fatal("ai_visible=false 应保留")
	}
	// 未知工具
	c, _, _ = execTool(mkToolCall("nope", `{}`), cb, &res)
	if c != `{"error":"unknown tool"}` {
		t.Fatalf("未知工具: %q", c)
	}
}

func mkToolCall(name, args string) llmToolCall {
	return llmToolCall{ID: "c1", Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: name, Arguments: args}}
}

func TestIsToolResultTurn(t *testing.T) {
	m := anthropicMessage{Role: "user", Content: []anthropicBlock{{Type: "tool_result"}}}
	if !isToolResultTurn(m) {
		t.Fatal("纯 tool_result 应 true")
	}
	m.Content = append(m.Content, anthropicBlock{Type: "text"})
	if isToolResultTurn(m) {
		t.Fatal("混合块应 false")
	}
	if isToolResultTurn(anthropicMessage{}) {
		t.Fatal("空内容应 false")
	}
}

func TestAnthropicErrMsg(t *testing.T) {
	if got := anthropicErrMsg([]byte(`{"error":{"message":"boom"}}`)); got != "boom" {
		t.Fatalf("error 提取错误: %q", got)
	}
	if got := anthropicErrMsg([]byte("plain text")); got != "plain text" {
		t.Fatalf("非 JSON 应原样: %q", got)
	}
}

func TestToAnthropicMessages(t *testing.T) {
	msgs := []llmMessage{
		{Role: "system", Content: "SYS"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", ToolCalls: []llmToolCall{{
			ID: "t1", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "search_entries", Arguments: ""}, // 空 args 补 {}
		}}},
		{Role: "tool", ToolCallID: "t1", Content: `{"results":[]}`},
		{Role: "tool", ToolCallID: "t2", Content: "second"}, // 连续 tool 合并
		{Role: "assistant", Content: "done"},
	}
	sys, out := toAnthropicMessages(msgs)
	if sys != "SYS" {
		t.Fatalf("system 抽取错误: %q", sys)
	}
	if len(out) != 4 { // user(hi), assistant(tool_use), user(tool_result x2), assistant(text)
		t.Fatalf("消息数错误: %d", len(out))
	}
	// assistant 轮：空文本只有 tool_use 块
	if len(out[1].Content) != 1 || out[1].Content[0].Type != "tool_use" || string(out[1].Content[0].Input) != "{}" {
		t.Fatalf("tool_use 块错误: %+v", out[1].Content)
	}
	// 两条 tool 结果合并在同一 user 轮
	if len(out[2].Content) != 2 || out[2].Content[0].Type != "tool_result" || out[2].Content[1].ToolUseID != "t2" {
		t.Fatalf("tool_result 合并错误: %+v", out[2].Content)
	}
	// tool 结果后接普通文本 user 轮（不合并）
	if len(out) == 4 && out[3].Role != "assistant" {
		t.Fatal("末轮应为 assistant")
	}
}

func TestCallLLMErrorShapes(t *testing.T) {
	// HTTP 500 带 error message
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer s1.Close()
	_, err := callLLM(context.Background(), Config{BaseURL: s1.URL}, nil)
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("500 应带 error message: %v", err)
	}
	// 200 但非 JSON
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer s2.Close()
	if _, err := callLLM(context.Background(), Config{BaseURL: s2.URL}, nil); err == nil {
		t.Fatal("非 JSON 应报错")
	}
	// 200 空 choices
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer s3.Close()
	if _, err := callLLM(context.Background(), Config{BaseURL: s3.URL}, nil); err == nil {
		t.Fatal("空 choices 应报错")
	}
}

// Chat 工具循环超限：mock 永远要求 search → 4 轮后报错
func TestChatToolLoopLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		args, _ := json.Marshal(map[string]any{"query": "x"})
		resp := map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"tool_calls": []any{map[string]any{
				"id": "c", "type": "function",
				"function": map[string]any{"name": "search_entries", "arguments": string(args)},
			}}},
			"finish_reason": "tool_calls",
		}}}
		b, _ := json.Marshal(resp)
		w.Write(b)
	}))
	defer s.Close()
	cb := ToolCallbacks{SearchEntries: func(q string) ([]model.Entry, error) { return nil, nil }}
	_, err := Chat(context.Background(), Config{BaseURL: s.URL}, "sys", nil, "q", cb)
	if err == nil || !strings.Contains(err.Error(), "4 轮") {
		t.Fatalf("应报工具循环超限: %v", err)
	}
}

// ListModels OpenAI 404 → /v1 重试
func TestListModelsV1Fallback(t *testing.T) {
	calls := []string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.URL.Path == "/models" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer s.Close()
	ids, err := ListModels(context.Background(), Config{BaseURL: s.URL, APIKey: "k"})
	if err != nil || len(ids) != 1 || ids[0] != "m1" {
		t.Fatalf("应回退 /v1/models: %v %v", ids, err)
	}
	if len(calls) != 2 {
		t.Fatalf("应请求两次: %v", calls)
	}
	// 空清单也继续试下一个地址
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"data":[]}`)) // 空清单
			return
		}
		w.Write([]byte(`{"data":[{"id":"m2"}]}`))
	}))
	defer s2.Close()
	ids, err = ListModels(context.Background(), Config{BaseURL: s2.URL})
	if err != nil || len(ids) != 1 {
		t.Fatalf("空清单应继续尝试: %v %v", ids, err)
	}
	// 全部失败 → 错误
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer s3.Close()
	if _, err := ListModels(context.Background(), Config{BaseURL: s3.URL}); err == nil {
		t.Fatal("全失败应报错")
	}
}

// callAnthropic 非流式成功 + 流式 SSE
func TestCallAnthropic(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-a" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("端点错误: %s", r.URL.Path)
		}
		var req anthropicRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			write := func(v any) {
				b, _ := json.Marshal(v)
				w.Write([]byte("data: " + string(b) + "\n\n"))
			}
			write(map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "tool_use", "id": "tu1", "name": "save_entry_draft"}})
			write(map[string]any{"type": "content_block_delta", "index": 0,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"title":"T"`}})
			write(map[string]any{"type": "content_block_delta", "index": 0,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": `,"category":"misc","fields":[]}`}})
			write(map[string]any{"type": "content_block_delta", "index": 1,
				"delta": map[string]any{"type": "text_delta", "text": "好的"}})
			fl.Flush()
			return
		}
		w.Write([]byte(`{"content":[{"type":"text","text":"回复"},{"type":"tool_use","id":"tu9","name":"search_entries","input":{"query":"q"}}],"stop_reason":"tool_use"}`))
	}))
	defer s.Close()
	cfg := Config{Protocol: ProtocolAnthropic, BaseURL: s.URL, APIKey: "sk-a", Model: "claude"}

	// 非流式
	choice, err := callAnthropic(context.Background(), cfg, []llmMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Message.Content != "回复" || len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("非流式 choice 错误: %+v", choice)
	}
	if choice.Message.ToolCalls[0].Function.Name != "search_entries" || choice.FinishReason != "tool_calls" {
		t.Fatalf("tool_calls 还原错误: %+v", choice.Message.ToolCalls[0])
	}

	// 流式：text_delta + tool_use 分片
	var deltas []string
	choice, err = callAnthropic(context.Background(), cfg, []llmMessage{{Role: "user", Content: "hi"}},
		func(ev StreamEvent) error {
			if ev.Type == "reply_delta" {
				deltas = append(deltas, ev.Text)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Message.Content != "好的" || len(deltas) != 1 {
		t.Fatalf("流式文本错误: %q %v", choice.Message.Content, deltas)
	}
	if len(choice.Message.ToolCalls) != 1 || choice.Message.ToolCalls[0].Function.Arguments != `{"title":"T","category":"misc","fields":[]}` {
		t.Fatalf("流式 tool args 拼接错误: %+v", choice.Message.ToolCalls)
	}

	// 认证失败
	_, err = callAnthropic(context.Background(), Config{Protocol: ProtocolAnthropic, BaseURL: s.URL, APIKey: "bad"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("401 应带错误信息: %v", err)
	}
}

// anthropic 坏响应形态
func TestCallAnthropicBadResponses(t *testing.T) {
	// 200 非 JSON
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("garbage"))
	}))
	defer s1.Close()
	if _, err := callAnthropic(context.Background(), Config{BaseURL: s1.URL}, nil, nil); err == nil {
		t.Fatal("非 JSON 应报错")
	}
	// 200 但 content 空
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[]}`))
	}))
	defer s2.Close()
	if _, err := callAnthropic(context.Background(), Config{BaseURL: s2.URL}, nil, nil); err == nil {
		t.Fatal("空 content 应报错")
	}
	// 200 带 error 字段
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[{"type":"text","text":"x"}],"error":{"message":"oops"}}`))
	}))
	defer s3.Close()
	if _, err := callAnthropic(context.Background(), Config{BaseURL: s3.URL}, nil, nil); err == nil {
		t.Fatal("error 字段应报错")
	}
}

// ChatStream 工具循环超限（流式版）
func TestChatStreamToolLoopLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		args, _ := json.Marshal(map[string]any{"query": "x"})
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{"index": 0, "id": "c",
				"function": map[string]any{"name": "search_entries", "arguments": string(args)}}}}}}})
		w.Write([]byte("data: " + string(b) + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer s.Close()
	cb := ToolCallbacks{SearchEntries: func(q string) ([]model.Entry, error) { return nil, nil }}
	_, err := ChatStream(context.Background(), Config{BaseURL: s.URL}, "sys", nil, "q", cb, nil)
	if err == nil || !strings.Contains(err.Error(), "4 轮") {
		t.Fatalf("流式也应超限报错: %v", err)
	}
}


func TestFetchModelsBadJSON(t *testing.T) {
	// 200 但坏 JSON → 报错
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	}))
	defer s.Close()
	if _, err := listModelsOpenAI(context.Background(), Config{BaseURL: s.URL}); err == nil {
		t.Fatal("坏 JSON 模型清单应报错")
	}
}
