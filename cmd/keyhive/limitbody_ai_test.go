package main

// 【回归锚点｜/api/v1/ai 请求体限制】
// 对应 docs/review/04-role-review-r1-security.md N2：limitBody 原先只挂在
// human.Routes()（/api/v1 人用子树），main.go 独立挂载的 /api/v1/ai（Bearer token
// AI API）不经过它，aiapi.Reveal 裸解码 body——持 reveal token 者可投递无限大 JSON。
// 修复：api.LimitBody 挂到 main.go 顶层 router，AI 子树同样 10MB 上限。
// 正确行为：>10MB 请求体对 AI 子树返回 413（在鉴权/解码之前）；同路径小请求体
// 正常进入后续中间件（无 token → 401），证明 413 由体积触发而非路径被整体封禁。

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAISubtreeBodyLimit(t *testing.T) {
	env := newE2E(t)
	aiBase := env.srv.URL + "/api/v1/ai"

	// 11MB 合法 JSON（超 10MB 上限）：413 必须发生在鉴权与业务解码之前
	big := `{"field":"` + strings.Repeat("a", 11<<20) + `"}`
	req, _ := http.NewRequest("POST", aiBase+"/entries/1/reveal", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer whatever-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("[N2] >10MB 请求体打到 AI 子树应 413，实际 %d", resp.StatusCode)
	}

	// 对照：同路径小请求体（无 token）→ 401，说明 413 是体积触发、路径可达
	req2, _ := http.NewRequest("POST", aiBase+"/entries/1/reveal",
		strings.NewReader(`{"field":"password"}`))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("对照组：小请求体无 token 应 401，实际 %d", resp2.StatusCode)
	}
}
