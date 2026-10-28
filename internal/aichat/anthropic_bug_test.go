package aichat

// 【缺陷复现测试｜预期红】
// 对应 docs/review/01-review-ai-integration.md P2-1（internal/aichat/anthropic.go:281-309）：
// parseAnthropicStream 的事件 switch 只有 content_block_start / content_block_delta 两个 case，
// Anthropic 官方流在中途出错（限流/credit 不足/overload）会发送 {"type":"error",...} 事件，
// 该事件被静默吞掉，流正常 EOF 后返回空 choice，ChatStream 表现为“空回复 + 成功”。
// 正确行为：流中出现 error 事件时 ChatStream 应返回非 nil error。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBug_AnthropicStreamErrorPropagated：流中 error 事件必须作为错误返回，
// 不得表现为“成功 + 部分/空回复”
func TestBug_AnthropicStreamErrorPropagated(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		seq := []string{
			`{"type":"message_start","message":{}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"已解析"}}`,
			`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded latency, try again"}}`,
			`{"type":"message_stop"}`,
		}
		for _, s := range seq {
			fmt.Fprintf(w, "data: %s\n\n", s)
			fl.Flush()
		}
	}))
	t.Cleanup(ts.Close)

	var deltas []string
	resp, err := ChatStream(context.Background(), Config{
		Protocol: ProtocolAnthropic, BaseURL: ts.URL, APIKey: "k", Model: "claude-sonnet-4-5",
	}, "sys", nil, "存 ACR", cbWith(nil), func(ev StreamEvent) error {
		if ev.Type == "reply_delta" {
			deltas = append(deltas, ev.Text)
		}
		return nil
	})
	if err == nil {
		t.Fatalf("流中出现 error 事件时应返回错误，实际成功返回：reply=%q deltas=%v resp=%+v",
			replyOf(resp), deltas, resp)
	}
	if !strings.Contains(err.Error(), "verloaded") {
		t.Logf("提示：错误信息未携带上游 message（非阻断）: %v", err)
	}
}

func replyOf(r *Response) string {
	if r == nil {
		return "<nil>"
	}
	return r.Reply
}
