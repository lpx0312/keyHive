package mcpserver

// ---------------------------------------------------------------------------
// 缺陷复现测试（预期红）：以下用例断言"正确行为"，在当前生产代码下会失败。
// 对应 docs/review/01-review-mcp-importer.md 的 P3 发现，生产代码修复后应转绿。
//
//   - TestBug_MCPErrorFlagged  mcp.go:130-139
// ---------------------------------------------------------------------------

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestBug_MCPErrorFlagged P3：callAndWrap 对 err 与非 200 分支一律
// NewToolResultText，isError 标志缺失。MCP 客户端（ZCode/Claude 等）依赖
// isError 判断工具是否失败，"HTTP 404: ..."之类的文本会被当作正常返回值
// 进入模型上下文。
// 正确行为：错误结果带 isError=true（mcp-go v1.1.1 的 CallToolResult.IsError，
// json tag "isError"，可用 mcp.NewToolResultError 构造）。
func TestBug_MCPErrorFlagged(t *testing.T) {
	// kh_get 不存在条目：服务端 404 → 工具结果应为错误结果
	setup(t, goodCfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"条目不存在"}`))
	}))

	res, err := toolGet(context.Background(), callArgs(t, map[string]any{"id": "999"}))
	if err != nil {
		t.Fatal(err)
	}
	// 错误文本仍应完整透传（现状行为，保持）
	if txt := resultText(t, res); !strings.Contains(txt, "HTTP 404") {
		t.Fatalf("错误文本应含 HTTP 404: %s", txt)
	}
	// 关键断言：MCP 层必须标记 isError，客户端才能区分成败
	if !res.IsError {
		t.Fatal("BUG mcp.go:130-139: 工具错误结果必须带 isError=true（CallToolResult.IsError），当前用 NewToolResultText 返回，MCP 客户端无法区分成败")
	}

	// callAndWrap 的 err 分支同样应标记 isError
	res2, _ := callAndWrap(func() ([]byte, int, error) { return nil, 0, os.ErrClosed })
	if !res2.IsError {
		t.Fatal("BUG mcp.go:130-139: callAndWrap 的 err 分支同样必须带 isError=true")
	}

	// 对照：成功结果不应误标 isError（现状行为，保持绿）
	ok, _ := callAndWrap(func() ([]byte, int, error) { return []byte(`{"ok":1}`), 200, nil })
	if ok.IsError {
		t.Fatal("成功结果不应标记 isError")
	}
}
