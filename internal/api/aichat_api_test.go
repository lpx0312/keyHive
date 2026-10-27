package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockLLM 起一个 OpenAI 兼容的假 LLM 服务：
//   - 非流式 /chat/completions：返回 cannedChoices（可含 tool_calls）
//   - 流式 /chat/completions：按 SSE 逐字下发 replyText，再下发 toolCalls 片段，最后 [DONE]
//   - /models：返回 modelIDs
type mockLLM struct {
	srv         *httptest.Server
	replyText   string
	toolCalls   []map[string]any // 完整 tool_calls（非流式用）
	modelIDs    []string
	lastAuth    string
	lastReqBody map[string]any
}

func newMockLLM(t *testing.T, reply string, tools []map[string]any, models []string) *mockLLM {
	t.Helper()
	m := &mockLLM{replyText: reply, toolCalls: tools, modelIDs: models}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		m.lastAuth = r.Header.Get("Authorization")
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		m.lastReqBody = req
		if stream, _ := req["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			// 逐字下发回复文本
			for _, seg := range []string{m.replyText[:len(m.replyText)/2], m.replyText[len(m.replyText)/2:]} {
				chunk := map[string]any{"choices": []any{map[string]any{
					"delta": map[string]any{"content": seg}}}}
				b, _ := json.Marshal(chunk)
				w.Write([]byte("data: " + string(b) + "\n\n"))
				fl.Flush()
			}
			// tool_calls 分片下发
			for i, tc := range m.toolCalls {
				fn := tc["function"].(map[string]any)
				head := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
					"tool_calls": []any{map[string]any{"index": 0, "id": tc["id"], "function": map[string]any{"name": fn["name"], "arguments": ""}}}}}}}
				b, _ := json.Marshal(head)
				w.Write([]byte("data: " + string(b) + "\n\n"))
				_ = i
				args, _ := json.Marshal(fn["arguments"].(map[string]any))
				tail := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
					"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": string(args)}}}}}}}
				b2, _ := json.Marshal(tail)
				w.Write([]byte("data: " + string(b2) + "\n\n"))
			}
			w.Write([]byte("data: [DONE]\n\n"))
			fl.Flush()
			return
		}
		msg := map[string]any{"content": m.replyText}
		if len(m.toolCalls) > 0 {
			// OpenAI 协议里 function.arguments 是 JSON 字符串而非对象
			tcs := make([]any, 0, len(m.toolCalls))
			for _, tc := range m.toolCalls {
				fn := tc["function"].(map[string]any)
				argsStr, _ := json.Marshal(fn["arguments"])
				tcs = append(tcs, map[string]any{
					"id": tc["id"], "type": "function",
					"function": map[string]any{"name": fn["name"], "arguments": string(argsStr)},
				})
			}
			msg["tool_calls"] = tcs
		}
		resp := map[string]any{"choices": []any{map[string]any{"message": msg, "finish_reason": "stop"}}}
		b, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		m.lastAuth = r.Header.Get("Authorization")
		if m.modelIDs == nil { // 模拟端点不存在
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"message":"not found"}}`))
			return
		}
		data := []any{}
		for _, id := range m.modelIDs {
			data = append(data, map[string]any{"id": id})
		}
		b, _ := json.Marshal(map[string]any{"data": data})
		w.Write(b)
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// aiSetup 登录 admin 并把 LLM 配置指向 mock server
func aiSetup(t *testing.T, baseURL, apiKey string) (*http.Client, string) {
	t.Helper()
	srv, _ := newHumanServer(t)
	t.Cleanup(srv.Close)
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	if apiKey != "" {
		if code, out := do(t, a, "PUT", base+"/ai-config",
			`{"protocol":"openai","base_url":`+jsonString(baseURL)+`,"api_key":`+jsonString(apiKey)+`,"model":"mock-model"}`); code != 200 {
			t.Fatalf("配置 LLM 失败: %d %v", code, out)
		}
	}
	return a, base
}

// ---- AI 配置读写 ----

func TestAIConfigGetDefaults(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	code, out := do(t, a, "GET", base+"/ai-config", "")
	if code != 200 {
		t.Fatalf("GET 应 200，实际 %d", code)
	}
	if out["protocol"] != "openai" || out["base_url"] == "" || out["model"] == "" {
		t.Fatalf("默认配置异常: %v", out)
	}
	if out["configured"] != "false" || out["api_key"] != "" {
		t.Fatalf("未配置时 api_key 应空: %v", out)
	}
	if _, ok := out["presets"].([]any); !ok {
		t.Fatalf("应返回预设列表: %v", out["presets"])
	}
}

func TestAIConfigSaveAndMask(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	// 坏 JSON / 非法 protocol
	if code, _ := do(t, a, "PUT", base+"/ai-config", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "PUT", base+"/ai-config", `{"protocol":"gemini"}`); code != 400 {
		t.Fatalf("非法 protocol 应 400，实际 %d", code)
	}

	// 保存完整配置
	if code, out := do(t, a, "PUT", base+"/ai-config",
		`{"protocol":"anthropic","base_url":"https://api.anthropic.com","api_key":"sk-ant-12345678","model":"claude-3"}`); code != 200 || out["ok"] != true {
		t.Fatalf("保存应 200: %d %v", code, out)
	}
	// 回读：遮蔽 + configured
	_, out := do(t, a, "GET", base+"/ai-config", "")
	if out["api_key"] != "****5678" {
		t.Fatalf("key 应遮蔽为 ****5678: %v", out["api_key"])
	}
	if out["configured"] != "true" || out["protocol"] != "anthropic" || out["model"] != "claude-3" {
		t.Fatalf("回读配置异常: %v", out)
	}
	// key 是密文落库
	var enc string
	fx.DB.QueryRow(`SELECT value FROM settings WHERE key='ai_llm_api_key'`).Scan(&enc)
	if enc == "" || strings.Contains(enc, "sk-ant-12345678") {
		t.Fatalf("key 应加密存储: %q", enc)
	}
	// 审计
	var n int
	fx.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='ai_chat_config'`).Scan(&n)
	if n != 1 {
		t.Fatalf("应记 ai_chat_config 审计，实际 %d", n)
	}

	// **** 前缀回传值不覆盖已存 key；其余字段可单独更新
	if code, _ := do(t, a, "PUT", base+"/ai-config", `{"api_key":"****5678","model":"claude-3.5"}`); code != 200 {
		t.Fatalf("遮蔽 key 回传应 200，实际 %d", code)
	}
	_, out = do(t, a, "GET", base+"/ai-config", "")
	if out["model"] != "claude-3.5" || out["api_key"] != "****5678" {
		t.Fatalf("key 不应被遮蔽值覆盖: %v", out)
	}
	// 空 key 也不动
	if code, _ := do(t, a, "PUT", base+"/ai-config", `{"model":"claude-4"}`); code != 200 {
		t.Fatalf("空 key 更新应 200，实际 %d", code)
	}
	_, out = do(t, a, "GET", base+"/ai-config", "")
	if out["api_key"] != "****5678" || out["configured"] != "true" {
		t.Fatalf("空 key 不应清除已存 key: %v", out)
	}
}

// ---- 测试连接 / 模型列表 ----

func TestAIConfigTest(t *testing.T) {
	ml := newMockLLM(t, "pong 回复", nil, nil)
	// 未填 key 且系统未配置
	a, base := aiSetup(t, ml.srv.URL, "")
	if code, out := do(t, a, "POST", base+"/ai-config/test", `{}`); code != 200 || out["ok"] != false {
		t.Fatalf("无 key 应 ok=false: %d %v", code, out)
	}
	// 表单带 key：直连 mock 成功
	code, out := do(t, a, "POST", base+"/ai-config/test",
		`{"protocol":"openai","base_url":`+jsonString(ml.srv.URL)+`,"api_key":"sk-form","model":"m1"}`)
	if code != 200 || out["ok"] != true {
		t.Fatalf("表单 key 测试应 ok=true: %d %v", code, out)
	}
	if out["protocol"] != "openai" || out["model"] != "m1" {
		t.Fatalf("应回显 protocol/model: %v", out)
	}
	if _, ok := out["latency_ms"]; !ok {
		t.Fatalf("应含 latency_ms: %v", out)
	}
	if ml.lastAuth != "Bearer sk-form" {
		t.Fatalf("应用表单 key 鉴权: %q", ml.lastAuth)
	}
	// 表单空 key 回退已保存 key
	a2, base2 := aiSetup(t, ml.srv.URL, "sk-saved-key")
	code, out = do(t, a2, "POST", base2+"/ai-config/test", `{"api_key":""}`)
	if code != 200 || out["ok"] != true {
		t.Fatalf("回退保存 key 应 ok=true: %d %v", code, out)
	}
	if ml.lastAuth != "Bearer sk-saved-key" {
		t.Fatalf("应回退保存 key: %q", ml.lastAuth)
	}
	// mock 挂掉 → ok=false
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer dead.Close()
	code, out = do(t, a2, "POST", base2+"/ai-config/test",
		`{"base_url":`+jsonString(dead.URL)+`,"api_key":"sk-x"}`)
	if code != 200 || out["ok"] != false || out["error"] == "" {
		t.Fatalf("失败端点应 ok=false: %d %v", code, out)
	}
}

func TestAIListModels(t *testing.T) {
	ml := newMockLLM(t, "", nil, []string{"glm-4.6", "glm-4.5-air"})
	a, base := aiSetup(t, ml.srv.URL, "sk-k")

	if code, _ := do(t, a, "POST", base+"/ai-config/models", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	code, out := do(t, a, "POST", base+"/ai-config/models", `{"base_url":`+jsonString(ml.srv.URL)+`,"api_key":"sk-k"}`)
	if code != 200 || out["ok"] != true {
		t.Fatalf("拉模型应 ok=true: %d %v", code, out)
	}
	models, _ := out["models"].([]any)
	if len(models) != 2 || models[0] != "glm-4.5-air" {
		t.Fatalf("模型清单应排序返回: %v", models)
	}
	// 404 端点 → ok=false
	ml2 := newMockLLM(t, "", nil, nil)
	code, out = do(t, a, "POST", base+"/ai-config/models",
		`{"base_url":`+jsonString(ml2.srv.URL)+`,"api_key":"sk-k"}`)
	if code != 200 || out["ok"] != false || out["error"] == "" {
		t.Fatalf("404 端点应 ok=false: %d %v", code, out)
	}
}

// ---- 对话（非流式）----

func TestAIChatNonStream(t *testing.T) {
	// text 校验 / 未配置
	a, base := aiSetup(t, "http://unused.local", "")
	if code, _ := do(t, a, "POST", base+"/ai-chat", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/ai-chat", `{"text":"  "}`); code != 400 {
		t.Fatalf("空 text 应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/ai-chat", `{"text":"hi"}`); code != 400 {
		t.Fatalf("未配置应 400，实际 %d", code)
	}

	// 纯文本回复
	ml := newMockLLM(t, "这是助手回复", nil, nil)
	a, base = aiSetup(t, ml.srv.URL, "sk-chat")
	code, out := do(t, a, "POST", base+"/ai-chat", `{"text":"帮我记一条"}`)
	if code != 200 || out["reply"] != "这是助手回复" {
		t.Fatalf("对话应 200: %d %v", code, out)
	}
	if out["draft"] != nil {
		t.Fatalf("无工具调用不应有草稿: %v", out)
	}
	// 请求体带 system 提示词与 tools
	if ml.lastReqBody["tools"] == nil {
		t.Fatalf("请求应携带 tools: %v", ml.lastReqBody)
	}

	// 带草稿：save_entry_draft 工具
	tool := map[string]any{
		"id": "call-1", "type": "function",
		"function": map[string]any{
			"name": "save_entry_draft",
			"arguments": map[string]any{
				"title": "新ACR", "category": "docker_registry", "ai_visible": true,
				"fields": []any{map[string]any{"key": "password", "description": "密码", "value": "pw", "type": "text", "is_secret": true}},
			},
		},
	}
	ml2 := newMockLLM(t, "", []map[string]any{tool}, nil)
	a, base = aiSetup(t, ml2.srv.URL, "sk-chat2")
	srv := strings.TrimSuffix(base, "/api/v1")
	_ = srv
	code, out = do(t, a, "POST", base+"/ai-chat", `{"text":"记一条 ACR 密码 pw"}`)
	if code != 200 {
		t.Fatalf("草稿对话应 200: %d %v", code, out)
	}
	if out["draft_kind"] != "create" || out["draft"] == nil {
		t.Fatalf("应返回 create 草稿: %v", out)
	}
	draft := out["draft"].(map[string]any)
	if draft["title"] != "新ACR" {
		t.Fatalf("草稿标题错误: %v", draft)
	}

	// 后端挂掉 → 502
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer dead.Close()
	a3, base3 := aiSetup(t, dead.URL, "sk-x")
	if code, _ := do(t, a3, "POST", base3+"/ai-chat", `{"text":"hi"}`); code != 502 {
		t.Fatalf("LLM 故障应 502，实际 %d", code)
	}
}

// ---- 对话（流式 SSE）----

func TestAIChatStream(t *testing.T) {
	a0, base0 := aiSetup(t, "http://unused.local", "")
	if code, _ := do(t, a0, "POST", base0+"/ai-chat/stream", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	if code, _ := do(t, a0, "POST", base0+"/ai-chat/stream", `{"text":""}`); code != 400 {
		t.Fatalf("空 text 应 400，实际 %d", code)
	}
	if code, _ := do(t, a0, "POST", base0+"/ai-chat/stream", `{"text":"hi"}`); code != 400 {
		t.Fatalf("未配置应 400，实际 %d", code)
	}

	ml := newMockLLM(t, "流式回复内容", nil, nil)
	a, base := aiSetup(t, ml.srv.URL, "sk-s")
	resp, err := a.Post(base+"/ai-chat/stream", "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("流式应 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 SSE: %q", ct)
	}
	events := readSSE(t, resp.Body)
	// 事件序列：reply_delta（逐段）… done 收尾
	var types []string
	for _, ev := range events {
		types = append(types, ev["type"].(string))
	}
	if len(types) < 2 || types[0] != "reply_delta" || types[len(types)-1] != "done" {
		t.Fatalf("事件序列应为 reply_delta...done，实际 %v", types)
	}
	var full strings.Builder
	for _, ev := range events {
		if ev["type"] == "reply_delta" {
			full.WriteString(ev["text"].(string))
		}
	}
	if full.String() != "流式回复内容" {
		t.Fatalf("delta 拼接应还原全文: %q", full.String())
	}
	if events[len(events)-1]["type"] != "done" {
		t.Fatalf("最后事件应为 done: %v", events[len(events)-1])
	}
}

func TestAIChatStreamWithDraft(t *testing.T) {
	tool := map[string]any{
		"id": "call-2", "type": "function",
		"function": map[string]any{
			"name": "update_entry_draft",
			"arguments": map[string]any{
				"entry_id": 7, "title": "改标题", "category": "misc",
				"fields": []any{map[string]any{"key": "k", "description": "d", "value": "v"}},
			},
		},
	}
	ml := newMockLLM(t, "", []map[string]any{tool}, nil)
	a, base := aiSetup(t, ml.srv.URL, "sk-s")
	resp, err := a.Post(base+"/ai-chat/stream", "application/json", strings.NewReader(`{"text":"改一下"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := readSSE(t, resp.Body)
	var hasDraft, hasDone bool
	for _, ev := range events {
		switch ev["type"] {
		case "draft":
			hasDraft = true
			if ev["draft_kind"] != "update" {
				t.Fatalf("draft_kind 应 update: %v", ev)
			}
			if ev["draft_entry_id"].(float64) != 7 {
				t.Fatalf("entry_id 应 7: %v", ev)
			}
		case "done":
			hasDone = true
		}
	}
	if !hasDraft || !hasDone {
		t.Fatalf("应含 draft 与 done 事件: %v", events)
	}
}

// readSSE 逐条解析 data: {...} 事件
func readSSE(t *testing.T, body interface{ Read([]byte) (int, error) }) []map[string]any {
	t.Helper()
	var events []map[string]any
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(payload), &ev) == nil {
			events = append(events, ev)
		}
	}
	return events
}
