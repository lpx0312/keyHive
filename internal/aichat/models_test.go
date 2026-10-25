package aichat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// OpenAI 协议直连：base 以 /v1 结尾 → GET /v1/models（不再拼别的路径）
func TestListModelsOpenAIDirect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "b"}, {"id": "a"}}})
	}))
	t.Cleanup(ts.Close)

	got, err := ListModels(context.Background(), Config{Protocol: ProtocolOpenAI, BaseURL: ts.URL + "/v1", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("结果不符（应排序去重）: %v", got)
	}
}

// OpenAI 协议回退：根地址不带 /v1 且 /models 404 时自动补 /v1/models 重试
func TestListModelsOpenAIFallbackV1(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-chat"}, {"id": "deepseek-reasoner"}}})
	}))
	t.Cleanup(ts.Close)

	got, err := ListModels(context.Background(), Config{Protocol: ProtocolOpenAI, BaseURL: ts.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "deepseek-chat" {
		t.Fatalf("回退 /v1/models 未生效: %v", got)
	}
}

// Anthropic 协议：x-api-key 认证 + has_more/last_id 分页跟随
func TestListModelsAnthropicPaginated(t *testing.T) {
	pages := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(401)
			return
		}
		pages++
		if r.URL.Query().Get("after") == "" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "claude-c"}}, "has_more": true, "last_id": "claude-c"})
			return
		}
		if r.URL.Query().Get("after") != "claude-c" {
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "claude-a"}, {"id": "claude-b"}}, "has_more": false})
	}))
	t.Cleanup(ts.Close)

	got, err := ListModels(context.Background(), Config{Protocol: ProtocolAnthropic, BaseURL: ts.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 {
		t.Fatalf("应请求两页，实际 %d", pages)
	}
	if len(got) != 3 || got[0] != "claude-a" || got[2] != "claude-c" {
		t.Fatalf("分页结果不符: %v", got)
	}
}

// 预设清单基本约束：字段齐全、协议合法、无重名
func TestPresetsValid(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Presets {
		if p.Name == "" || p.BaseURL == "" {
			t.Fatalf("预设缺 Name/BaseURL: %+v", p)
		}
		if p.Protocol != ProtocolOpenAI && p.Protocol != ProtocolAnthropic {
			t.Fatalf("预设协议非法: %s %q", p.Name, p.Protocol)
		}
		if seen[p.Name] {
			t.Fatalf("预设重名: %s", p.Name)
		}
		seen[p.Name] = true
	}
	if len(Presets) < 10 {
		t.Fatalf("预设数量过少: %d", len(Presets))
	}
}
