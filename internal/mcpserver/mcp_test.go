package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// setup 配置指向 mock 服务的环境（KEYHIVE_CONFIG + KEYHIVE_BASE_URL）
func setup(t *testing.T, cfgJSON string, handler http.HandlerFunc) {
	t.Helper()
	if handler != nil {
		ts := httptest.NewServer(handler)
		t.Cleanup(ts.Close)
		t.Setenv("KEYHIVE_BASE_URL", ts.URL)
	} else {
		t.Setenv("KEYHIVE_BASE_URL", "http://127.0.0.1:1")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if cfgJSON != "" {
		os.WriteFile(p, []byte(cfgJSON), 0o600)
	}
	t.Setenv("KEYHIVE_CONFIG", p)
}

// callArgs 构造带参数的 MCP 请求
func callArgs(t *testing.T, args map[string]any) mcp.CallToolRequest {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

const goodCfg = `{"base_url":"http://x","token_read":"kh_r","token_reveal":"kh_v"}`

func aiHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/ai/entries") && r.Header.Get("Authorization") == "Bearer kh_r":
		w.Write([]byte(`[{"id":1,"title":"ACR","fields":[{"key":"password","description":"密码","is_secret":true,"value":"***"}]}]`))
	case strings.HasSuffix(r.URL.Path, "/ai/search"):
		w.Write([]byte(`[{"id":1,"title":"ACR"}]`))
	case strings.HasSuffix(r.URL.Path, "/ai/entries/1") && r.Header.Get("Authorization") == "Bearer kh_r":
		w.Write([]byte(`{"id":1,"title":"ACR","fields":[]}`))
	case strings.HasSuffix(r.URL.Path, "/reveal") && r.Header.Get("Authorization") == "Bearer kh_v":
		w.Write([]byte(`{"entry_title":"ACR","value":"plain-pw"}`))
	default:
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"令牌无效"}`))
	}
}

func TestMCPTools(t *testing.T) {
	ctx := context.Background()
	setup(t, goodCfg, aiHandler)

	// kh_status
	res, err := toolStatus(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if txt := resultText(t, res); !strings.Contains(txt, "ACR") {
		t.Fatalf("kh_status 输出错误: %s", txt)
	}

	// kh_list（带/不带 category）
	res, _ = toolList(ctx, callArgs(t, map[string]any{"category": "docker_registry"}))
	if txt := resultText(t, res); !strings.Contains(txt, "ACR") {
		t.Fatalf("kh_list 输出错误: %s", txt)
	}
	res, _ = toolList(ctx, mcp.CallToolRequest{})
	if txt := resultText(t, res); !strings.Contains(txt, "ACR") {
		t.Fatalf("kh_list 无参数输出错误: %s", txt)
	}

	// kh_search
	res, _ = toolSearch(ctx, callArgs(t, map[string]any{"query": "ACR"}))
	if txt := resultText(t, res); !strings.Contains(txt, "ACR") {
		t.Fatalf("kh_search 输出错误: %s", txt)
	}
	// 缺 query
	res, _ = toolSearch(ctx, mcp.CallToolRequest{})
	if txt := resultText(t, res); !strings.Contains(txt, "缺少 query") {
		t.Fatalf("缺 query 应提示: %s", txt)
	}

	// kh_get
	res, _ = toolGet(ctx, callArgs(t, map[string]any{"id": "1"}))
	if txt := resultText(t, res); !strings.Contains(txt, "ACR") {
		t.Fatalf("kh_get 输出错误: %s", txt)
	}
	res, _ = toolGet(ctx, mcp.CallToolRequest{})
	if txt := resultText(t, res); !strings.Contains(txt, "缺少 id") {
		t.Fatalf("缺 id 应提示: %s", txt)
	}

	// kh_reveal
	res, _ = toolReveal(ctx, callArgs(t, map[string]any{"id": "1", "field": "password"}))
	if txt := resultText(t, res); !strings.Contains(txt, "plain-pw") {
		t.Fatalf("kh_reveal 输出错误: %s", txt)
	}
	res, _ = toolReveal(ctx, callArgs(t, map[string]any{"field": "password"}))
	if txt := resultText(t, res); !strings.Contains(txt, "缺少 id") {
		t.Fatalf("reveal 缺 id 应提示: %s", txt)
	}
	res, _ = toolReveal(ctx, callArgs(t, map[string]any{"id": "1"}))
	if txt := resultText(t, res); !strings.Contains(txt, "缺少 field") {
		t.Fatalf("reveal 缺 field 应提示: %s", txt)
	}
}

func TestMCPConfigErrors(t *testing.T) {
	ctx := context.Background()

	// token_read 未配置
	setup(t, `{"token_read":"","token_reveal":"kh_v"}`, aiHandler)
	res, _ := toolList(ctx, mcp.CallToolRequest{})
	if txt := resultText(t, res); !strings.Contains(txt, "token_read 未配置") {
		t.Fatalf("应提示 token_read 未配置: %s", txt)
	}
	res, _ = toolSearch(ctx, callArgs(t, map[string]any{"query": "x"}))
	if txt := resultText(t, res); !strings.Contains(txt, "token_read 未配置") {
		t.Fatalf("search 应提示未配置: %s", txt)
	}
	res, _ = toolGet(ctx, callArgs(t, map[string]any{"id": "1"}))
	if txt := resultText(t, res); !strings.Contains(txt, "token_read 未配置") {
		t.Fatalf("get 应提示未配置: %s", txt)
	}

	// token_reveal 未配置
	setup(t, `{"token_read":"kh_r","token_reveal":""}`, aiHandler)
	res, _ = toolReveal(ctx, callArgs(t, map[string]any{"id": "1", "field": "password"}))
	if txt := resultText(t, res); !strings.Contains(txt, "token_reveal 未配置") {
		t.Fatalf("reveal 应提示未配置: %s", txt)
	}

	// 坏配置文件
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte("{bad"), 0o600)
	t.Setenv("KEYHIVE_CONFIG", p)
	res, _ = toolStatus(ctx, mcp.CallToolRequest{})
	if txt := resultText(t, res); !strings.Contains(txt, "格式错误") {
		t.Fatalf("坏配置应报错: %s", txt)
	}
	res, _ = toolReveal(ctx, callArgs(t, map[string]any{"id": "1", "field": "password"}))
	if txt := resultText(t, res); txt == "" {
		t.Fatal("reveal 坏配置应有错误文本")
	}

	// 服务不可达 / 令牌无效（401）
	setup(t, goodCfg, aiHandler) // aiHandler 对 entries 无 kh_r 的返回 401 —— 这里用无效令牌
	setup2 := func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"令牌无效"}`))
		}))
		t.Cleanup(ts.Close)
		t.Setenv("KEYHIVE_BASE_URL", ts.URL)
	}
	_ = setup2
	_ = json.Marshal
	// callAndWrap 分支直接单测
}

func TestCallAndWrap(t *testing.T) {
	// err 分支
	res, _ := callAndWrap(func() ([]byte, int, error) { return nil, 0, os.ErrClosed })
	if txt := resultText(t, res); !strings.Contains(txt, "请求失败") {
		t.Fatalf("err 分支错误: %s", txt)
	}
	// 非 200 分支
	res, _ = callAndWrap(func() ([]byte, int, error) { return []byte("nope"), 401, nil })
	if txt := resultText(t, res); !strings.Contains(txt, "HTTP 401") || !strings.Contains(txt, "nope") {
		t.Fatalf("非 200 分支错误: %s", txt)
	}
	// 成功分支
	res, _ = callAndWrap(func() ([]byte, int, error) { return []byte(`{"ok":1}`), 200, nil })
	if txt := resultText(t, res); !strings.Contains(txt, `"ok"`) {
		t.Fatalf("成功分支错误: %s", txt)
	}
	// text()
	if resultText(t, text("hi")) != "hi" {
		t.Fatal("text() 输出错误")
	}
}

func TestRunServeStdioEOF(t *testing.T) {
	setup(t, "", nil)
	// stdin 立即 EOF：ServeStdio 正常返回 → Run 退出 0 或 1（取决于 EOF 处理），都不应 panic/阻塞
	old := os.Stdin
	r, w, _ := os.Pipe()
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old }()
	done := make(chan int, 1)
	go func() { done <- Run() }()
	select {
	case rc := <-done:
		if rc != 0 && rc != 1 {
			t.Fatalf("Run 退出码异常: %d", rc)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run 应在 stdin EOF 后退出")
	}
}
