package aichat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"keyhive/internal/model"
)

// sseMock 模拟流式 LLM：轮次脚本，每轮一串 SSE chunk
type sseTurn struct {
	contentDeltas []string                        // 回复文本增量
	toolCall      *mockStreamTool                 // 工具调用（分片发送 arguments）
}

type mockStreamTool struct {
	name string
	args []string // arguments 分片
}

func sseServer(t *testing.T, turns []sseTurn) *httptest.Server {
	t.Helper()
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		if i >= len(turns) {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"(脚本耗尽)\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			fl.Flush()
			return
		}
		turn := turns[i]
		i++
		if r.Body != nil {
			r.ParseForm() // 丢弃
		}
		for _, d := range turn.contentDeltas {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", quote(d))
			fl.Flush()
		}
		if turn.toolCall != nil {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":%s,\"arguments\":\"\"}}]}}]}\n\n", quote(turn.toolCall.name))
			fl.Flush()
			for _, a := range turn.toolCall.args {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":%s}}]}}]}\n\n", quote(a))
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
}

func quote(s string) string {
	return fmt.Sprintf("%q", s)
}

func TestChatStreamReplyDeltas(t *testing.T) {
	ts := sseServer(t, []sseTurn{{contentDeltas: []string{"密码", "是多", "少？"}}})
	var events []StreamEvent
	resp, err := ChatStream(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "x",
		ToolCallbacks{}, func(ev StreamEvent) error { events = append(events, ev); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reply != "密码是多少？" {
		t.Fatalf("回复累积错误: %q", resp.Reply)
	}
	var got []string
	for _, e := range events {
		if e.Type == "reply_delta" {
			got = append(got, e.Text)
		}
	}
	if strings.Join(got, "") != "密码是多少？" || len(got) != 3 {
		t.Fatalf("delta 事件不符: %v", got)
	}
}

func TestChatStreamAgentFlowWithFragmentedToolArgs(t *testing.T) {
	draft := `{"entry_id":5,"title":"华为SWR","category":"huawei_swr","fields":[{"key":"password","description":"密码","is_secret":true,"value":"***"}]}`
	mid := len(draft) / 2 // 切成两半，验证 arguments 分片拼接
	ts := sseServer(t, []sseTurn{
		{toolCall: &mockStreamTool{name: "search_entries", args: []string{`{"quer`, `y":"swr"}`}}},
		{contentDeltas: []string{"已找到"}, toolCall: &mockStreamTool{name: "update_entry_draft", args: []string{draft[:mid], draft[mid:]}}},
	})
	lib := []model.Entry{{ID: 5, Title: "华为SWR", Category: "huawei_swr", AIVisible: true}}
	var statuses []string
	resp, err := ChatStream(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "补说明",
		cbWith(lib), func(ev StreamEvent) error {
			if ev.Type == "status" {
				statuses = append(statuses, ev.Text)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Draft == nil || resp.DraftKind != DraftUpdate || resp.DraftEntryID != 5 {
		t.Fatalf("分片拼接后草稿不符: kind=%s id=%d", resp.DraftKind, resp.DraftEntryID)
	}
	if len(statuses) < 2 || !strings.Contains(statuses[0], "查询") || !strings.Contains(statuses[len(statuses)-1], "草稿") {
		t.Fatalf("status 事件序列不符: %v", statuses)
	}
}

func TestChatStreamEmitBackpressure(t *testing.T) {
	// emit 返回错误应中断（SSE 客户端断开场景）
	ts := sseServer(t, []sseTurn{{contentDeltas: []string{"a", "b", "c"}}})
	_, err := ChatStream(context.Background(), Config{BaseURL: ts.URL, APIKey: "k"}, "sys", nil, "x",
		ToolCallbacks{}, func(ev StreamEvent) error { return fmt.Errorf("client gone") })
	if err == nil || !strings.Contains(err.Error(), "client gone") {
		t.Fatalf("emit 错误应中断: %v", err)
	}
}
