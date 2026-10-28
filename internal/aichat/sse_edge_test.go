package aichat

// SSE 解析畸形输入与上游错误传播的盲区补测（对应 docs/review/01-review-ai-integration.md 测试盲区：
// 半行 chunk / CRLF / 空 data / 超大单行 / 上游 5xx / 上下文超时）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rawSSEServer 返回原始字节的 SSE 上游：status!=200 时返回 JSON 错误体，否则依次写出 chunks
func rawSSEServer(t *testing.T, chunks []string, status int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":{"message":"upstream boom"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			w.Write([]byte(c))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func collectDeltas() func(StreamEvent) error {
	var got []string
	return func(ev StreamEvent) error {
		if ev.Type == "reply_delta" {
			got = append(got, ev.Text)
		}
		return nil
	}
}

// OpenAI 流：CRLF 行尾 / 空 data / 空白 data / 非 data 行 / 坏 JSON / 空 choices / 错误对象
// 均应被跳过不崩溃；EOF 处无换行的半行 chunk 仍应被解析
func TestStreamMalformedSSEOpenAI(t *testing.T) {
	chunks := []string{
		"event: ping\n",                                                    // 非 data 行
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\r\n\r\n",      // CRLF 行尾
		"data:\n\n",                                                         // 空 data
		"data:   \n\n",                                                      // 空白 data
		"data: not-json\n\n",                                                // 坏 JSON
		"data: {\"choices\":[]}\n\n",                                        // 空 choices（跳过）
		"data: {\"error\":{\"message\":\"mid-stream err\"}}\n\n",            // 错误对象（当前按空 choices 跳过）
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}",             // EOF 半行（无换行结尾）
	}
	ts := rawSSEServer(t, chunks, 200)
	choice, err := callLLMStream(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"},
		[]llmMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("畸形输入不应报错: %v", err)
	}
	if choice.Message.Content != "ab" {
		t.Fatalf("半行 chunk 应被解析，期望回复 \"ab\"，实际 %q", choice.Message.Content)
	}
}

// OpenAI 流：单行超过 1MB 行缓冲上限应报“读取流失败”，不能吞掉
func TestStreamOversizeLineOpenAI(t *testing.T) {
	huge := "data: " + strings.Repeat("x", 2<<20) + "\n\n" // 2MB > 1MB 上限
	ts := rawSSEServer(t, []string{huge, "data: {\"choices\":[{\"delta\":{\"content\":\"z\"}}]}\n\n"}, 200)
	_, err := callLLMStream(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"},
		[]llmMessage{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "读取流失败") {
		t.Fatalf("超大单行应触发行缓冲上限错误: %v", err)
	}
}

// Anthropic 流：CRLF / 空 data / 坏 JSON 跳过，EOF 半行仍解析
func TestParseAnthropicStreamMalformed(t *testing.T) {
	input := strings.Join([]string{
		"event: ping\n",
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"你\"}}\r\n\r\n",
		"data:\n\n",
		"data: not-json\n\n",
		"data: {\"type\":\"unknown_event\",\"x\":1}\n\n",                         // 未知事件类型（跳过）
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"好\"}}", // EOF 半行
	}, "")
	emit := collectDeltas()
	choice, err := parseAnthropicStream(strings.NewReader(input), emit)
	if err != nil {
		t.Fatalf("畸形输入不应报错: %v", err)
	}
	if choice.Message.Content != "你好" {
		t.Fatalf("半行 chunk 应被解析，期望 \"你好\"，实际 %q", choice.Message.Content)
	}
}

// Anthropic 流：超大单行同样触发上限错误
func TestParseAnthropicStreamOversizeLine(t *testing.T) {
	huge := "data: " + strings.Repeat("y", 2<<20) + "\n\n"
	_, err := parseAnthropicStream(strings.NewReader(huge), func(StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "读取流失败") {
		t.Fatalf("超大单行应触发行缓冲上限错误: %v", err)
	}
}

// 上游 5xx：两协议流式路径都应返回携带状态码的错误
func TestStreamUpstream5xxPropagates(t *testing.T) {
	msgs := []llmMessage{{Role: "user", Content: "hi"}}
	ts := rawSSEServer(t, nil, 500)
	_, err := callLLMStream(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, msgs, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("OpenAI 流式 5xx 应报错: %v", err)
	}
	ts2 := rawSSEServer(t, nil, 503)
	_, err = callAnthropic(context.Background(), Config{Protocol: ProtocolAnthropic, BaseURL: ts2.URL, APIKey: "k"},
		msgs, func(StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("Anthropic 流式 5xx 应报错: %v", err)
	}
}

// 上游阻塞 + 上下文超时：两协议流式路径都应把取消/超时作为错误返回（客户端断开传导链路）
func TestStreamContextDeadlinePropagates(t *testing.T) {
	slow := func() *httptest.Server {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(1 * time.Second) // 模拟上游挂起
		}))
		return ts
	}
	msgs := []llmMessage{{Role: "user", Content: "hi"}}

	ts := slow()
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := callLLMStream(ctx, Config{BaseURL: ts.URL, APIKey: "k"}, msgs, nil)
	if err == nil || !strings.Contains(err.Error(), "调用 LLM 失败") {
		t.Fatalf("OpenAI 流式上下文超时应报错: %v", err)
	}

	ts2 := slow()
	t.Cleanup(ts2.Close)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel2()
	_, err = callAnthropic(ctx2, Config{Protocol: ProtocolAnthropic, BaseURL: ts2.URL, APIKey: "k"},
		msgs, func(StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "调用 LLM 失败") {
		t.Fatalf("Anthropic 流式上下文超时应报错: %v", err)
	}
}
