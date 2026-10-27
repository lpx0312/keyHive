package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFieldValidType(t *testing.T) {
	valid := []string{"text", "url", "multiline", ""}
	for _, v := range valid {
		if !(Field{Type: v}).ValidType() {
			t.Errorf("%q 应为合法类型", v)
		}
	}
	if (Field{Type: "json"}).ValidType() {
		t.Error("json 应为非法类型")
	}
}

func TestFieldNormalize(t *testing.T) {
	f := Field{Key: "  host ", Description: " 地址\t", Type: ""}
	f.Normalize()
	if f.Key != "host" || f.Description != "地址" || f.Type != FieldTypeText {
		t.Errorf("Normalize 结果错误: %+v", f)
	}
}

func TestEntryFieldByKeyAndMask(t *testing.T) {
	e := &Entry{Fields: []Field{
		{Key: "user", Value: "u"},
		{Key: "password", IsSecret: true, Value: "real-pw"},
	}}
	if e.FieldByKey("user") == nil || e.FieldByKey("nope") != nil {
		t.Fatal("FieldByKey 查找错误")
	}
	e.MaskSecrets()
	if e.Fields[0].Value != "u" {
		t.Fatal("非敏感值不应遮蔽")
	}
	if e.Fields[1].Value != MaskedValue {
		t.Fatal("敏感值应遮蔽为 ***")
	}
}

func TestValidate(t *testing.T) {
	// 正常：trim + 默认 category + tags 归一化
	e := &Entry{Title: "  T  ", Category: "  ", Tags: []string{"a", " a ", "", "b"}}
	if msg := e.Validate(); msg != "" {
		t.Fatalf("应通过校验: %s", msg)
	}
	if e.Title != "T" || e.Category != "misc" {
		t.Fatalf("trim/默认分类错误: %+v", e)
	}
	if len(e.Tags) != 2 || e.Tags[0] != "a" || e.Tags[1] != "b" {
		t.Fatalf("tags 归一化错误: %v", e.Tags)
	}
	// 各失败分支
	cases := []struct {
		e    Entry
		want string
	}{
		{Entry{Title: ""}, "标题不能为空"},
		{Entry{Title: "t", Fields: []Field{{Key: " "}}}, "未填写名称"},
		{Entry{Title: "t", Fields: []Field{{Key: "a"}, {Key: "a"}}}, "重复"},
		{Entry{Title: "t", Fields: []Field{{Key: "a", Type: "bad"}}}, "类型无效"},
	}
	for i, c := range cases {
		if msg := c.e.Validate(); msg == "" || !strings.Contains(msg, c.want) {
			t.Errorf("case %d 应报 %q，实际 %q", i, c.want, msg)
		}
	}
	// 字段名 trim 后参与查重
	e2 := &Entry{Title: "t", Fields: []Field{{Key: " a"}, {Key: "a "}}}
	if msg := e2.Validate(); !strings.Contains(msg, "重复") {
		t.Errorf("trim 后重复应报错: %q", msg)
	}
}

func TestEntryJSONShape(t *testing.T) {
	// omitempty 行为：空 value 不出现在 JSON（模板/遮蔽形态）
	b, _ := json.Marshal(Field{Key: "k"})
	if strings.Contains(string(b), "value") {
		t.Fatalf("空 value 应省略: %s", b)
	}
	// tags omitempty
	b2, _ := json.Marshal(Entry{Title: "t"})
	if strings.Contains(string(b2), "tags") {
		t.Fatalf("空 tags 应省略: %s", b2)
	}
}

func TestAPITokenHelpers(t *testing.T) {
	tk := &APIToken{Scopes: []string{"read", "reveal"}}
	if !tk.HasScope("read") || tk.HasScope("admin") {
		t.Fatal("HasScope 错误")
	}
	if !tk.Effective() {
		t.Fatal("未吊销应有效")
	}
	now := "2026-01-01T00:00:00Z"
	tk.RevokedAt = &now
	if tk.Effective() {
		t.Fatal("吊销后应无效")
	}
}
