package aiapi

// 【缺陷复现测试｜预期红】
// 对应 docs/review/01-review-ai-integration.md P2-2（internal/aiapi/aiapi.go:126-127）：
// reveal 的审计 detail 为手工拼接 JSON（`{"title":"`+e.Title+`","field":"`+f.Key+`"}`），
// Entry.Validate 对 Title 仅 TrimSpace、对 Field.Key 无字符限制，两者均可含 " 与 \，
// title 为 a"b 时 detail 是无效 JSON（审计页解析失败）；title 形如
// x","field":"password2 时可伪造审计中显示的字段名。
// 正确行为：detail 必须是合法 JSON（json.Unmarshal 可解析且字段值保真）。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lpx0312/keyHive/internal/model"
)

// TestBug_RevealAuditDetailValidJSON：标题含双引号的条目走 reveal 后，
// field_reveal 审计的 detail 必须可被 json.Unmarshal 解析，且 title/field 值保真
func TestBug_RevealAuditDetailValidJSON(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()

	const quotedTitle = `prod "root" 账号`
	e := mustCreateEntry(t, fx, model.Entry{
		Title: quotedTitle, Category: "misc", AIVisible: true,
		Fields: []model.Field{{Key: "password", Description: "密码", Type: "text", IsSecret: true, Value: "v"}},
	})

	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/ai/entries/"+itoa(e.ID)+"/reveal",
		strings.NewReader(`{"field":"password"}`))
	req.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("reveal 应 200，实际 %d", resp.StatusCode)
	}

	var detail string
	if err := fx.DB.QueryRow(
		`SELECT detail FROM audit_logs WHERE action = ? ORDER BY id DESC LIMIT 1`,
		model.ActionFieldReveal).Scan(&detail); err != nil {
		t.Fatalf("未找到 field_reveal 审计: %v", err)
	}

	var parsed map[string]string
	if jerr := json.Unmarshal([]byte(detail), &parsed); jerr != nil {
		t.Fatalf("审计 detail 不是合法 JSON（title 含双引号即畸形）: %q，解析错误: %v", detail, jerr)
	}
	if parsed["title"] != quotedTitle {
		t.Fatalf("审计 detail 的 title 应保真: got %q want %q", parsed["title"], quotedTitle)
	}
	if parsed["field"] != "password" {
		t.Fatalf("审计 detail 的 field 应保真: got %q want %q", parsed["field"], "password")
	}
}
