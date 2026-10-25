package aichat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTestConnection 返回字段：ok/model/protocol/latency_ms；长中文回复按 rune 截断不产生乱码
func TestTestConnectionFields(t *testing.T) {
	longReply := strings.Repeat("连通正常，我随时待命。", 10) // 110 runes，超 60 触发截断
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
			"message": map[string]any{"content": longReply}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(ts.Close)

	res := TestConnection(context.Background(), Config{BaseURL: ts.URL, Model: "glm-4.6"})
	if res["ok"] != true {
		t.Fatalf("应成功: %+v", res)
	}
	if res["protocol"] != ProtocolOpenAI {
		t.Fatalf("协议不符: %v", res["protocol"])
	}
	if ms, ok := res["latency_ms"].(int64); !ok || ms < 0 {
		t.Fatalf("latency_ms 缺失/非法: %v", res["latency_ms"])
	}
	reply, _ := res["reply"].(string)
	if !strings.HasSuffix(reply, "…") || len([]rune(reply)) != 61 {
		t.Fatalf("应按 rune 截断到 60+省略号: %d runes, %q", len([]rune(reply)), reply)
	}
	if strings.ContainsRune(reply, 0xFFFD) {
		t.Fatalf("截断产生乱码: %q", reply)
	}
}

// Anthropic 协议测试路径：protocol 字段正确
func TestTestConnectionAnthropic(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "pong"}}, "stop_reason": "end_turn"})
	}))
	t.Cleanup(ts.Close)

	res := TestConnection(context.Background(), Config{Protocol: ProtocolAnthropic, BaseURL: ts.URL, Model: "claude-sonnet-4-5"})
	if res["ok"] != true || res["protocol"] != ProtocolAnthropic {
		t.Fatalf("结果不符: %+v", res)
	}
	if ms, ok := res["latency_ms"].(int64); !ok || ms < 0 {
		t.Fatalf("latency_ms 缺失/非法: %v", res["latency_ms"])
	}
}

// 失败路径：ok=false 时仍带 latency_ms 与 error
func TestTestConnectionError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"密钥无效"}}`))
	}))
	t.Cleanup(ts.Close)

	res := TestConnection(context.Background(), Config{BaseURL: ts.URL, Model: "m"})
	if res["ok"] != false {
		t.Fatalf("应失败: %+v", res)
	}
	if res["latency_ms"] == nil || res["error"] == nil {
		t.Fatalf("失败应带 latency_ms/error: %+v", res)
	}
	if !strings.Contains(res["error"].(string), "密钥无效") {
		t.Fatalf("错误信息不符: %v", res["error"])
	}
}
