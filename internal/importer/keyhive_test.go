package importer

import (
	"strings"
	"testing"
)

const keyhiveExport = `[
  {"id":7,"title":"ACR","category":"docker_registry","description":"阿里云容器镜像",
   "ai_visible":true,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",
   "fields":[
     {"key":"url","description":"网址","type":"url","is_secret":false,"value":"https://registry.cn-hangzhou.aliyuncs.com"},
     {"key":"username","description":"用户名","type":"text","is_secret":false,"value":"lipanx"},
     {"key":"password","description":"密码","type":"text","is_secret":true,"value":"real-pw"}
   ]},
  {"id":8,"title":"备注条目","category":"","description":"","ai_visible":false,
   "fields":[{"key":"notes","description":"备注","type":"multiline","is_secret":false,"value":"*** 心得"}]}
]`

func TestParseKeyhive(t *testing.T) {
	entries, err := ParseKeyhive([]byte(keyhiveExport))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("应解析 2 条，实际 %d", len(entries))
	}
	e := entries[0]
	if e.Title != "ACR" || e.Category != "docker_registry" || !e.AIVisible {
		t.Fatalf("业务字段应原样保留: %+v", e)
	}
	if e.ID != 0 || e.CreatedAt != "" || e.UpdatedAt != "" {
		t.Fatalf("元数据应清零: %+v", e)
	}
	if f := e.FieldByKey("password"); f == nil || !f.IsSecret || f.Value != "real-pw" {
		t.Fatalf("敏感字段应保留明文: %+v", e.Fields)
	}
	// 非敏感字段的字面 *** 不算遮蔽导出
	if entries[1].Fields[0].Value != "*** 心得" {
		t.Fatalf("非敏感字段值不应被改动: %+v", entries[1].Fields)
	}
}

func TestParseKeyhiveRejectsMasked(t *testing.T) {
	masked := `[{"title":"ACR","category":"docker_registry","fields":[
		{"key":"password","description":"密码","type":"text","is_secret":true,"value":"***"}]}]`
	_, err := ParseKeyhive([]byte(masked))
	if err == nil {
		t.Fatal("遮蔽导出应被拒绝")
	}
	if !strings.Contains(err.Error(), "***") || !strings.Contains(err.Error(), "明文") {
		t.Fatalf("报错应说明原因与出路: %v", err)
	}
}

func TestParseKeyhiveRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"空文件":  "  ",
		"单对象":  `{"title":"x","fields":[]}`,
		"垃圾":   `not json`,
		"空数组":  `[]`,
		"类型不对": `[1,2]`,
	}
	for name, data := range cases {
		if _, err := ParseKeyhive([]byte(data)); err == nil {
			t.Fatalf("%s 应报错", name)
		}
	}
}

func TestParseKeyhiveBOMAndEmptySecret(t *testing.T) {
	data := "\xEF\xBB\xBF" + `[{"title":"t","fields":[{"key":"pw","description":"","type":"text","is_secret":true}]}]`
	entries, err := ParseKeyhive([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Fields[0].Value != "" {
		t.Fatalf("BOM + 空敏感值应可导入: %+v", entries)
	}
}
