package importer

// 盲区/边界补测（应绿）：CSV 的 BOM/CRLF/空行/引号转义/内嵌换行/重复列名，
// keyhive JSON 的单条导入与部分遮蔽（*** 混真值）整体拒绝。

import (
	"strings"
	"testing"
)

func TestParseCSVBOMCRLFQuotedFields(t *testing.T) {
	// BOM + CRLF 行尾 + 字段含逗号/转义引号/内嵌换行（quoted multiline） + 空行
	csvData := []byte("\xEF\xBB\xBFname,login_uri,login_username,login_password,notes\r\n" +
		"\"Git, Hub\",https://github.com,\"me, x\",\"pw\"\"123\",\"恢复码:\n123-456\"\r\n" +
		"\r\n" +
		"Plain,https://p.com,u,pw2,\r\n")
	entries, err := Parse("bitwarden", csvData)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("应解析 2 条（CRLF 空行跳过），实际 %d", len(entries))
	}
	e := entries[0]
	if e.Title != "Git, Hub" {
		t.Fatalf("含逗号的引号字段应完整: %q", e.Title)
	}
	if e.Username != "me, x" {
		t.Fatalf("含逗号的用户名应完整: %q", e.Username)
	}
	if e.Password != `pw"123` {
		t.Fatalf("转义双引号应还原为单个: %q", e.Password)
	}
	if e.Notes != "恢复码:\n123-456" {
		t.Fatalf("quoted 内嵌换行应保留: %q", e.Notes)
	}
	if entries[1].Title != "Plain" || entries[1].Notes != "" {
		t.Fatalf("CRLF 普通行解析错误: %+v", entries[1])
	}
}

func TestParseCSVChromeCRLFAndBlankLines(t *testing.T) {
	// Chrome CSV：CRLF + 前后空行 + 多个连续空行
	csvData := []byte("\r\n" +
		"name,url,username,password\r\n" +
		"\r\n" +
		"A,u1,p1,x1\r\n" +
		"\r\n\r\n" +
		"B,u2,p2,x2\r\n" +
		"\r\n")
	entries, err := Parse("chrome", csvData)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Title != "A" || entries[1].Title != "B" {
		t.Fatalf("空行应全部跳过: %+v", entries)
	}
}

func TestParseCSVDuplicateColumnNames(t *testing.T) {
	// 重复列名：当前行为是"后列静默覆盖前列"（01-review-mcp-importer.md P3 已记录，
	// 不在本次缺陷复现清单内）。本测试锚定现状：解析不报错、值取最后一列，
	// 待导入器增加重复列名检测后应改为断言报错。
	csvData := []byte("name,name,login_password\nA,B,pw\n")
	entries, err := Parse("bitwarden", csvData)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("应解析 1 条，实际 %d", len(entries))
	}
	if entries[0].Title != "B" {
		t.Fatalf("重复列名当前为后列覆盖前列（锚定现状）: %+v", entries[0])
	}
	if entries[0].Password != "pw" {
		t.Fatalf("非重复列不受影响: %+v", entries[0])
	}
}

func TestParseKeyhiveSingleEntry(t *testing.T) {
	// 单条目库：元数据清零、字段原样保留
	data := `[{"id":42,"title":"唯一","category":"misc","description":"d","ai_visible":false,
	  "created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",
	  "fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"v"}]}]`
	entries, err := ParseKeyhive([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("应解析 1 条，实际 %d", len(entries))
	}
	e := entries[0]
	if e.Title != "唯一" || e.Category != "misc" || e.AIVisible {
		t.Fatalf("业务字段应原样保留: %+v", e)
	}
	if e.ID != 0 || e.CreatedAt != "" || e.UpdatedAt != "" {
		t.Fatalf("元数据应清零: %+v", e)
	}
	if f := e.FieldByKey("password"); f == nil || !f.IsSecret || f.Value != "v" {
		t.Fatalf("敏感字段应保留明文: %+v", e.Fields)
	}
}

func TestParseKeyhiveRejectsPartiallyMasked(t *testing.T) {
	// 同一条目内：敏感字段一个真值一个 ***，应整体拒绝
	mixedFields := `[{"title":"ACR","fields":[
		{"key":"username","type":"text","is_secret":false,"value":"u"},
		{"key":"password","type":"text","is_secret":true,"value":"real-pw"},
		{"key":"api_key","type":"text","is_secret":true,"value":"***"}]}]`
	_, err := ParseKeyhive([]byte(mixedFields))
	if err == nil {
		t.Fatal("真值混 *** 的部分遮蔽导出应整体拒绝")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("报错应指明遮蔽字段: %v", err)
	}

	// 多条目中后面的条目才出现遮蔽，同样整体拒绝且报错定位到具体条目
	laterMasked := `[{"title":"A","fields":[{"key":"p","type":"text","is_secret":true,"value":"v"}]},
		{"title":"B","fields":[{"key":"p","type":"text","is_secret":true,"value":"***"}]}]`
	_, err = ParseKeyhive([]byte(laterMasked))
	if err == nil {
		t.Fatal("第 N 条才出现遮蔽也应整体拒绝，不允许部分导入")
	}
	if !strings.Contains(err.Error(), "B") {
		t.Fatalf("报错应指明条目名: %v", err)
	}
}
