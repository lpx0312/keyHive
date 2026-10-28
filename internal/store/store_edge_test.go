package store

// 盲区/边界补测（应绿）：来自 10 路代码审查"测试盲区"一节——
// 五路搜索的 unicode/大小写/空串边界、删除空集/不存在 ID 的幂等性、
// 条目复制（含敏感字段遮蔽语义）、内置模板删除保护边界、无字段条目/空分类。

import (
	"testing"

	"github.com/lpx0312/keyHive/internal/crypto"
	"github.com/lpx0312/keyHive/internal/model"
)

// 五路搜索：title/description/category/fields/tags 全路径命中 + ASCII 大小写折叠 + unicode 精确匹配
func TestSearchUnicodeAndCaseBoundaries(t *testing.T) {
	st := newTestStore(t)
	seedEntry(t, st, "Nginx 生产节点", "server",
		model.Field{Key: "url", Type: "url", Value: "https://nginx.corp.internal"},
		model.Field{Key: "password", Type: "text", IsSecret: true, Value: "topsecret"})
	e2 := &model.Entry{
		Title: "MySQL 主库", Category: "database", Description: "反向代理的后端存储",
		Tags: []string{"内网", "生产"},
		Fields: []model.Field{{Key: "host", Type: "text", Value: "db-1"}},
	}
	if err := st.CreateEntry(e2); err != nil {
		t.Fatal(err)
	}

	must := func(q, cat string, want int) {
		t.Helper()
		list, err := st.ListEntries(q, cat)
		if err != nil {
			t.Fatalf("ListEntries(%q,%q) 报错: %v", q, cat, err)
		}
		if len(list) != want {
			t.Fatalf("ListEntries(%q,%q) 应命中 %d 条，实际 %d", q, cat, want, len(list))
		}
	}

	// 空串：全量
	must("", "", 2)
	// title：ASCII 大小写折叠（SQLite LIKE 对 ASCII 不敏感）
	must("nginx", "", 1)
	must("NGINX", "", 1)
	// title：unicode（中文）精确子串
	must("生产节点", "", 1)
	must("主库", "", 1)
	// description 路径
	must("反向代理", "", 1)
	// category 路径（含大小写折叠）
	must("DATABASE", "", 1)
	// fields 明文值路径（URL 大小写折叠）
	must("NGINX.CORP", "", 1)
	// tags JSON 路径
	must("内网", "", 1)
	// 中文关键词同时命中 title 与 tags
	must("生产", "", 2)
	// 组合：q 命中但 category 不匹配 → 空
	must("nginx", "database", 0)
	// 不存在的分类 → 空且非 nil
	list, _ := st.ListEntries("", "不存在的分类")
	if list == nil || len(list) != 0 {
		t.Fatalf("空结果应为 [] 非 nil: %v", list)
	}

	// 锚定已知限制（审查 P3）：SQLite LIKE 仅对 ASCII 大小写不敏感，
	// 非 ASCII 字母（如 É/é）不折叠——若未来改进为全 unicode 折叠，需同步更新本断言
	seedEntry(t, st, "Café 边界样本", "misc")
	must("CAFÉ", "", 0)
	must("Café", "", 1)
}

// 删除空集/不存在 ID：硬删除语义下幂等、无错、不影响其他条目
func TestDeleteEntryIdempotent(t *testing.T) {
	st := newTestStore(t)
	// 空表（空集）删除
	if err := st.DeleteEntry(42); err != nil {
		t.Fatalf("空表删除不存在 ID 应无错: %v", err)
	}
	e1 := seedEntry(t, st, "保留1", "misc")
	e2 := seedEntry(t, st, "保留2", "misc")
	// 删除不存在的 ID：静默幂等，其余条目不受影响
	if err := st.DeleteEntry(999); err != nil {
		t.Fatalf("删除不存在 ID 应无错: %v", err)
	}
	list, err := st.ListEntries("", "")
	if err != nil || len(list) != 2 {
		t.Fatalf("误删了别的条目: %v %d", err, len(list))
	}
	// 正常删除仍生效
	if err := st.DeleteEntry(e1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetEntry(e1.ID); err == nil {
		t.Fatal("删除后应查不到")
	}
	if _, err := st.GetEntry(e2.ID); err != nil {
		t.Fatal("相邻条目不应受影响")
	}
}

// 条目复制语义：读回明文 → 遮蔽视图不污染源 → 明文另存副本（重新加密、nonce 不同）→ *** 回传只作用于副本
func TestEntryCopySemantics(t *testing.T) {
	st := newTestStore(t)
	orig := &model.Entry{Title: "原始", Category: "web_account", Tags: []string{"内网"},
		Fields: []model.Field{
			{Key: "url", Type: "url", Value: "https://x.corp"},
			{Key: "password", Type: "text", IsSecret: true, Value: "原始密码"},
		}}
	if err := st.CreateEntry(orig); err != nil {
		t.Fatal(err)
	}

	// 复制起点：GetEntry 得到明文
	src, err := st.GetEntry(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 展示用遮蔽副本（深拷贝字段，避免共享底层数组）
	view := *src
	view.Fields = append([]model.Field(nil), src.Fields...)
	view.MaskSecrets()
	if view.FieldByKey("password").Value != model.MaskedValue {
		t.Fatal("遮蔽视图敏感值应为 ***")
	}
	if view.FieldByKey("url").Value != "https://x.corp" {
		t.Fatal("遮蔽不应影响非敏感字段")
	}
	if src.FieldByKey("password").Value != "原始密码" {
		t.Fatal("遮蔽副本不应污染源条目明文")
	}

	// 以明文另存副本（模拟"复制条目"落库路径）
	cp := *src
	cp.ID = 0
	cp.Title = "原始 (副本)"
	cp.Fields = append([]model.Field(nil), src.Fields...)
	if err := st.CreateEntry(&cp); err != nil {
		t.Fatal(err)
	}
	if cp.ID == orig.ID {
		t.Fatal("副本应获得新 ID")
	}
	got, err := st.GetEntry(cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FieldByKey("password").Value != "原始密码" {
		t.Fatal("副本敏感值 round-trip 失败")
	}

	// 副本与原件：敏感密文不同（随机 nonce）、非敏感明文一致、副本也加密落库
	var rawO, rawC string
	st.DB.QueryRow(`SELECT fields FROM entries WHERE id = ?`, orig.ID).Scan(&rawO)
	st.DB.QueryRow(`SELECT fields FROM entries WHERE id = ?`, cp.ID).Scan(&rawC)
	if vo, vc := extractValue(t, rawO, "password"), extractValue(t, rawC, "password"); vo == vc {
		t.Fatal("两次加密的密文应不同（随机 nonce）")
	} else if !crypto.IsEncrypted(vc) {
		t.Fatal("副本敏感值也应加密落库")
	}
	if extractValue(t, rawO, "url") != extractValue(t, rawC, "url") {
		t.Fatal("非敏感明文应一致")
	}

	// 副本上的 *** 回传更新：只保留副本原值，原件不受影响
	upd := model.Entry{Title: "原始 (副本) 改", Category: "web_account",
		Fields: []model.Field{
			{Key: "url", Type: "url", Value: "https://y.corp"},
			{Key: "password", Type: "text", IsSecret: true, Value: model.MaskedValue},
		}}
	if err := st.UpdateEntry(cp.ID, &upd); err != nil {
		t.Fatal(err)
	}
	got2, _ := st.GetEntry(cp.ID)
	if got2.FieldByKey("password").Value != "原始密码" {
		t.Fatal("*** 回传应保留副本原值")
	}
	if got2.FieldByKey("url").Value != "https://y.corp" {
		t.Fatal("非敏感字段应更新")
	}
	origGot, _ := st.GetEntry(orig.ID)
	if origGot.FieldByKey("url").Value != "https://x.corp" || origGot.FieldByKey("password").Value != "原始密码" {
		t.Fatal("副本更新不应影响原件")
	}
}

// 无字段条目：可创建、读回空字段列表、空分类默认 misc、出现在列表与分类中
func TestEntryWithoutFields(t *testing.T) {
	st := newTestStore(t)
	e := &model.Entry{Title: "无字段条目"} // Fields=nil、Category 空
	if err := st.CreateEntry(e); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetEntry(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fields) != 0 {
		t.Fatalf("无字段条目读回应为空字段列表: %d", len(got.Fields))
	}
	if got.Category != "misc" {
		t.Fatalf("空分类应默认 misc: %q", got.Category)
	}
	list, err := st.ListEntries("", "")
	if err != nil || len(list) != 1 {
		t.Fatalf("无字段条目应出现在列表: %v %d", err, len(list))
	}
	list, err = st.ListEntries("", "misc")
	if err != nil || len(list) != 1 {
		t.Fatalf("按默认分类筛选应命中: %v %d", err, len(list))
	}
	cats, err := st.Categories()
	if err != nil || len(cats) != 1 || cats[0] != "misc" {
		t.Fatalf("分类列表应只含默认 misc: %v %v", cats, err)
	}
}

// 模板增删改对内置模板的保护边界：builtin=1 不可删、可编辑；删除不存在 ID 幂等
func TestTemplateBuiltinProtection(t *testing.T) {
	st := tplSchema(t)
	// 直插一条 builtin=1 的内置模板（模拟种子数据形态）
	if _, err := st.DB.Exec(`INSERT INTO category_templates (category, name, group_name, fields, builtin, created_at, updated_at)
		VALUES ('ssh_server', 'SSH 服务器', '服务器/SSH', '[]', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	st.DB.QueryRow(`SELECT id FROM category_templates WHERE category = 'ssh_server'`).Scan(&id)

	// 内置模板删除被保护：不报错，但删不掉
	if err := st.DeleteTemplate(id); err != nil {
		t.Fatalf("删除内置模板不应报错: %v", err)
	}
	if _, err := st.GetTemplate(id); err != nil {
		t.Fatal("内置模板应受保护不可删除")
	}

	// 内置模板允许编辑（设计如此：改名/字段）
	tpl := &model.Template{ID: id, Category: "ssh_server", Name: "SSH 服务器 v2", Group: "服务器/SSH",
		Fields: []model.Field{{Key: "host", Type: "text"}}}
	if err := st.SaveTemplate(tpl); err != nil {
		t.Fatalf("内置模板编辑应被允许: %v", err)
	}
	got, _ := st.GetTemplate(id)
	if got.Name != "SSH 服务器 v2" {
		t.Fatal("内置模板编辑未生效")
	}

	// 删除不存在的模板 ID：静默幂等
	if err := st.DeleteTemplate(999); err != nil {
		t.Fatalf("删除不存在模板应为幂等无错: %v", err)
	}
	// 编辑不存在的模板 ID：仍应报错（与删除的幂等语义区分）
	if err := st.SaveTemplate(&model.Template{ID: 999, Name: "幽灵", Category: "ghost"}); err == nil {
		t.Fatal("编辑不存在模板应报错")
	}
}
