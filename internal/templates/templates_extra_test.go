package templates

// 盲区补测（应绿）：来自代码审查"测试盲区"一节——templates_test.go 的分组检查
// 只覆盖 8 组中的 5 组（缺 GroupMiddleware/GroupDevOps/GroupCert）；另补：
// 模板骨架实例化为条目必须可过 Validate（字段名不重复）、Builtin() 返回独立副本。

import (
	"testing"

	"github.com/lpx0312/keyHive/internal/model"
)

// 全部 8 个分组都必须有模板
func TestBuiltinAllGroupsCovered(t *testing.T) {
	groups := map[string]int{}
	for _, tpl := range Builtin() {
		groups[tpl.Group]++
	}
	all := []string{
		GroupCloud, GroupRegistry, GroupServer, GroupDB,
		GroupMiddleware, GroupDevOps, GroupCert, GroupGeneric,
	}
	for _, g := range all {
		if groups[g] == 0 {
			t.Errorf("缺少分组: %s", g)
		}
	}
	if len(groups) != len(all) {
		t.Errorf("出现了未定义分组: %v", groups)
	}
}

// 模板字段骨架填入占位值后实例化为条目，必须能通过 Validate：
// 字段名非空、模板内不重名、类型合法（重名模板会令基于它创建的条目永远校验失败）
func TestBuiltinSkeletonsPassEntryValidate(t *testing.T) {
	for _, tpl := range Builtin() {
		e := model.Entry{Title: "来自模板 " + tpl.Name, Category: tpl.Category}
		for _, f := range tpl.Fields {
			v := f
			v.Value = "占位值"
			e.Fields = append(e.Fields, v)
		}
		if msg := e.Validate(); msg != "" {
			t.Errorf("模板 %s 骨架无法通过条目校验: %s", tpl.Category, msg)
		}
	}
}

// Builtin() 每次返回独立副本：修改返回值不得污染种子数据（前端/种子共用同一来源）
func TestBuiltinReturnsFreshSlice(t *testing.T) {
	a := Builtin()
	if len(a) == 0 {
		t.Fatal("内置模板为空")
	}
	a[0].Name = "被污染的名字"
	a[0].Fields[0].Key = "polluted_key"
	b := Builtin()
	if b[0].Name == "被污染的名字" || b[0].Fields[0].Key == "polluted_key" {
		t.Fatal("Builtin() 应返回独立副本，对返回值的修改不应影响后续调用")
	}
}

// blank 自由定义起步模板：字段不应预设敏感位（用户自由填写，不默认遮蔽）
func TestBlankTemplateFieldsNotSecret(t *testing.T) {
	for _, tpl := range Builtin() {
		if tpl.Category != "blank" {
			continue
		}
		if len(tpl.Fields) == 0 {
			t.Fatal("blank 模板应至少含一个示范字段")
		}
		for _, f := range tpl.Fields {
			if f.IsSecret {
				t.Fatalf("blank 模板字段 %s 不应预设敏感位", f.Key)
			}
		}
		return
	}
	t.Fatal("应存在 blank 模板")
}
