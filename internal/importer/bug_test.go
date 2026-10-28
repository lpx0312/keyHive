package importer

// ---------------------------------------------------------------------------
// 缺陷复现测试（预期红）：以下用例断言"正确行为"，在当前生产代码下会失败。
// 对应 docs/review/01-review-mcp-importer.md 的 P1/P2 发现，生产代码修复后应转绿。
//
//   - TestBug_BitwardenNotesAreSecret  importer.go:110,115
//   - TestBug_MalformedCSVRejected      importer.go:57-64
// ---------------------------------------------------------------------------

import (
	"strings"
	"testing"
)

// TestBug_BitwardenNotesAreSecret P1：Bitwarden 的 notes 列是自由文本，
// 用户习惯存放恢复码/密保答案等高敏内容。当前 ToEntryJSON 把 notes 按非敏感
// 字段导入（is_secret=false）且条目强制 ai_visible=true，notes 明文直达
// AI 上下文，绕过遮蔽与 reveal 审计两级控制。
// 正确行为：notes 字段 is_secret=true，且含 notes 的条目 ai_visible=false。
func TestBug_BitwardenNotesAreSecret(t *testing.T) {
	e := Entry{Title: "GitHub", Username: "me@x.com", Password: "pw", Notes: "恢复码: 123-456"}
	m := ToEntryJSON(e, "Bitwarden")

	var notes map[string]any
	for _, f := range m["fields"].([]map[string]any) {
		if f["key"] == "notes" {
			notes = f
		}
	}
	if notes == nil {
		t.Fatal("notes 字段应存在")
	}
	if notes["is_secret"] != true {
		t.Fatalf("BUG importer.go:110: Bitwarden notes 含恢复码等敏感信息，应标记 is_secret=true，实际 %v", notes["is_secret"])
	}
	if m["ai_visible"] != false {
		t.Fatalf("BUG importer.go:115: 含 notes 的导入条目不应强制 ai_visible=true（当前明文直达 AI 上下文），实际 %v", m["ai_visible"])
	}
	// 对照：密码字段本就应标记敏感（现有行为，保持绿）
	for _, f := range m["fields"].([]map[string]any) {
		if f["key"] == "password" && f["is_secret"] != true {
			t.Fatal("password 字段应标记敏感")
		}
	}
}

// TestBug_MalformedCSVRejected P2：parseCSV 设置 LazyQuotes=true，未闭合引号的
// 畸形/截断 CSV 不报错，后续所有行被静默吞进前一条记录（bob 整条丢失、
// alice 的密码成为含换行的脏值）。密码管理器导入是"迁移后即删源"的高信任
// 操作，静默脏数据不可接受。
// 正确行为：畸形 CSV（未闭合引号）应返回 error，而非静默导入脏数据。
func TestBug_MalformedCSVRejected(t *testing.T) {
	data := []byte("name,login_username,login_password\n" +
		"alice,\"pw\n" +
		"bob,bobpw\n")
	entries, err := Parse("bitwarden", data)
	if err == nil {
		t.Fatalf("BUG importer.go:57-64: 未闭合引号的畸形 CSV 应报错拒绝，实际静默解析出 %d 条（bob 被吞、alice 密码为脏值 %+v）", len(entries), entries)
	}
	if !strings.Contains(err.Error(), "引号") && !strings.Contains(err.Error(), "quote") {
		t.Logf("报错信息最好指明引号问题: %v", err)
	}
}
