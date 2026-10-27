package templates

import (
	"strings"
	"testing"

	"github.com/lpx0312/keyHive/internal/model"
)

// Builtin() 是种子数据与前端选择器的唯一来源：
// 分类必须唯一、字段骨架必须可过 Validate 的类型校验、敏感字段必须有说明
func TestBuiltinIntegrity(t *testing.T) {
	tpls := Builtin()
	if len(tpls) < 10 {
		t.Fatalf("内置模板数量异常: %d", len(tpls))
	}
	seen := map[string]bool{}
	groups := map[string]bool{}
	for _, tpl := range tpls {
		if strings.TrimSpace(tpl.Category) == "" || strings.TrimSpace(tpl.Name) == "" {
			t.Fatalf("模板缺 category/name: %+v", tpl)
		}
		if seen[tpl.Category] {
			t.Fatalf("模板分类重复: %s", tpl.Category)
		}
		seen[tpl.Category] = true
		groups[tpl.Group] = true
		if len(tpl.Fields) == 0 {
			t.Fatalf("模板 %s 无字段", tpl.Name)
		}
		for _, f := range tpl.Fields {
			if !f.ValidType() {
				t.Fatalf("模板 %s 字段 %s 类型非法: %s", tpl.Name, f.Key, f.Type)
			}
			if f.Key == "" || f.Description == "" {
				t.Fatalf("模板 %s 字段缺 key/description: %+v", tpl.Name, f)
			}
		}
	}
	// 关键模板存在
	for _, want := range []string{"aliyun_aksk", "docker_registry", "ssh_server", "blank"} {
		if !seen[want] {
			t.Fatalf("缺少关键模板分类: %s", want)
		}
	}
	// 已知分组齐全
	for _, g := range []string{GroupCloud, GroupRegistry, GroupServer, GroupDB, GroupGeneric} {
		if !groups[g] {
			t.Fatalf("缺少分组: %s", g)
		}
	}
	// blank 模板（空白条目）特殊：允许 0 字段或极简
	var blank *model.Template
	for i := range tpls {
		if tpls[i].Category == "blank" {
			blank = &tpls[i]
		}
	}
	if blank == nil {
		t.Fatal("应存在 blank 模板")
	}
}

// 字段构造辅助函数输出正确形态（间接由 Builtin 覆盖，这里直接断言一次）
func TestFieldHelpers(t *testing.T) {
	if v := txt("k", "d"); v.Type != model.FieldTypeText || v.IsSecret {
		t.Errorf("t() 形态错误: %+v", v)
	}
	if v := tm("k", "d"); v.Type != model.FieldTypeMultiline || v.IsSecret {
		t.Errorf("tm() 形态错误: %+v", v)
	}
	if v := f("k", "d", true); v.Type != model.FieldTypeText || !v.IsSecret {
		t.Errorf("f() 形态错误: %+v", v)
	}
	if v := fu("k", "d"); v.Type != model.FieldTypeURL || v.IsSecret {
		t.Errorf("fu() 形态错误: %+v", v)
	}
	if v := fm("k", "d", true); v.Type != model.FieldTypeMultiline || !v.IsSecret {
		t.Errorf("fm() 形态错误: %+v", v)
	}
}

// txt 是对包内 t() 的别名包装（t 与 testing.T 命名冲突）
func txt(key, desc string) model.Field { return t(key, desc) }
