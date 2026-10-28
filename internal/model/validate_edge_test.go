package model

import (
	"fmt"
	"strings"
	"testing"
)

// 标题边界：纯空白标题报错；超长标题当前契约下放行（Validate 不设长度上限）
func TestValidateTitleBoundaries(t *testing.T) {
	if msg := (&Entry{Title: "   \t "}).Validate(); msg != "标题不能为空" {
		t.Fatalf("纯空白标题应报空标题: %q", msg)
	}
	long := strings.Repeat("超长", 5000) // 1 万字符
	e := &Entry{Title: long}
	if msg := e.Validate(); msg != "" {
		t.Fatalf("超长标题当前应放行: %q", msg)
	}
	if e.Title != long {
		t.Fatal("标题内容不应被截断")
	}
}

// 大量字段：300 个唯一字段全部通过且类型默认补齐；尾部落一个重复字段能被揪出
func TestValidateManyFields(t *testing.T) {
	fields := make([]Field, 300)
	for i := range fields {
		fields[i] = Field{Key: fmt.Sprintf("f%03d", i)}
	}
	e := &Entry{Title: "t", Fields: fields}
	if msg := e.Validate(); msg != "" {
		t.Fatalf("300 个唯一字段应通过: %q", msg)
	}
	for i := range e.Fields {
		if e.Fields[i].Type != FieldTypeText {
			t.Fatalf("字段 %d 类型应默认 text: %q", i, e.Fields[i].Type)
		}
	}

	dup := append(fields, Field{Key: "f299"}) // 与既有字段重复
	e2 := &Entry{Title: "t", Fields: dup}
	if msg := e2.Validate(); msg == "" || !strings.Contains(msg, "f299") {
		t.Fatalf("大量字段中的重复应报错并点名: %q", msg)
	}
}

// 字段名边界：查重区分大小写（当前契约）；键内空格/中文合法
func TestValidateFieldNameEdge(t *testing.T) {
	e := &Entry{Title: "t", Fields: []Field{
		{Key: "User"}, {Key: "user"}, // 大小写不同不算重复
		{Key: "host name"}, // 含内部空格合法
		{Key: "中文键"},
	}}
	if msg := e.Validate(); msg != "" {
		t.Fatalf("大小写不同/含空格/中文键应通过: %q", msg)
	}
}

// 空 Fields/Tags + Description trim：通过校验且不 panic，Tags 归一化为空切片
func TestValidateEmptyCollections(t *testing.T) {
	e := &Entry{Title: " t ", Description: "  描述  "}
	if msg := e.Validate(); msg != "" {
		t.Fatalf("无字段应通过: %q", msg)
	}
	if e.Title != "t" || e.Description != "描述" {
		t.Fatalf("标题/描述应 trim: %+v", e)
	}
	if e.Tags == nil || len(e.Tags) != 0 {
		t.Fatalf("nil tags 归一化应为空切片: %#v", e.Tags)
	}
	if e.Fields != nil {
		t.Fatalf("Fields 不应被修改: %#v", e.Fields)
	}
}

// tags 归一化：全重复只留一个；空白/中文混合按序去重
func TestValidateTagsDedupOrder(t *testing.T) {
	e := &Entry{Title: "t", Tags: []string{"x", " x", "x "}}
	if msg := e.Validate(); msg != "" {
		t.Fatal(msg)
	}
	if len(e.Tags) != 1 || e.Tags[0] != "x" {
		t.Fatalf("全重复 tags 应只剩 1 个: %v", e.Tags)
	}

	e2 := &Entry{Title: "t", Tags: []string{" 内网 ", "内网", "公网 ", "", "\t"}}
	e2.Validate()
	if len(e2.Tags) != 2 || e2.Tags[0] != "内网" || e2.Tags[1] != "公网" {
		t.Fatalf("混合 tags 应按序去重: %#v", e2.Tags)
	}
}
