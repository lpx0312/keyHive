package store

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/lpx0312/keyHive/internal/crypto"
	"github.com/lpx0312/keyHive/internal/model"
)

// tplDB 建含模板表的库
func tplSchema(t *testing.T) *Store {
	t.Helper()
	d := testDB(t)
	if _, err := d.Exec(`CREATE TABLE category_templates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		category TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		group_name TEXT NOT NULL DEFAULT '通用',
		fields TEXT NOT NULL DEFAULT '[]',
		builtin INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	st, err := NewForTest(d)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func seedEntry(t *testing.T, st *Store, title, category string, fields ...model.Field) *model.Entry {
	t.Helper()
	e := &model.Entry{Title: title, Category: category, Fields: fields}
	if err := st.CreateEntry(e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestListEntriesFiltering(t *testing.T) {
	st := newTestStore(t)
	seedEntry(t, st, "ACR 阿里云", "docker_registry",
		model.Field{Key: "url", Type: "url", Value: "https://acr.example"},
		model.Field{Key: "password", Type: "text", IsSecret: true, Value: "topsecret"})
	seedEntry(t, st, "内网Git", "web_account",
		model.Field{Key: "url", Type: "url", Value: "https://git.corp"})
	seedEntry(t, st, "MySQL 生产", "database")

	// 全量
	list, err := st.ListEntries("", "")
	if err != nil || len(list) != 3 {
		t.Fatalf("全量应 3 条: %v %d", err, len(list))
	}
	// title 模糊
	list, _ = st.ListEntries("Git", "")
	if len(list) != 1 || list[0].Title != "内网Git" {
		t.Fatalf("按标题搜索错误: %+v", list)
	}
	// fields 非敏感值可搜
	list, _ = st.ListEntries("git.corp", "")
	if len(list) != 1 {
		t.Fatalf("按 URL 搜索应命中: %d", len(list))
	}
	// 敏感值不可搜（密文）
	list, _ = st.ListEntries("topsecret", "")
	if len(list) != 0 {
		t.Fatalf("敏感值不应可搜: %d", len(list))
	}
	// category 精确
	list, _ = st.ListEntries("", "web_account")
	if len(list) != 1 {
		t.Fatalf("分类筛选应 1 条: %d", len(list))
	}
	// q + category 组合
	list, _ = st.ListEntries("Git", "database")
	if len(list) != 0 {
		t.Fatalf("组合筛选应为空: %d", len(list))
	}
	// 无结果时返回 []（非 nil）
	list, _ = st.ListEntries("不存在的关键词", "")
	if list == nil || len(list) != 0 {
		t.Fatalf("空结果应为 [] 非 nil: %v", list)
	}
}

func TestEntryNotFoundAndDelete(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetEntry(999); err == nil {
		t.Fatal("GetEntry 不存在应报错")
	}
	e := seedEntry(t, st, "待删", "misc")
	if err := st.DeleteEntry(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetEntry(e.ID); err == nil {
		t.Fatal("删除后应查不到")
	}
	// UpdateEntry 不存在
	upd := model.Entry{Title: "x", Fields: nil}
	if err := st.UpdateEntry(999, &upd); err == nil {
		t.Fatal("UpdateEntry 不存在应报错")
	}
	// UpdateEntry 校验失败
	e2 := seedEntry(t, st, "保留", "misc")
	bad := model.Entry{Title: "  ", Fields: nil}
	if err := st.UpdateEntry(e2.ID, &bad); err == nil {
		t.Fatal("UpdateEntry 空标题应报错")
	}
}

func TestCategories(t *testing.T) {
	st := newTestStore(t)
	// 空库 → [] 非 nil
	cats, err := st.Categories()
	if err != nil || cats == nil || len(cats) != 0 {
		t.Fatalf("空库分类应为 []: %v %v", cats, err)
	}
	seedEntry(t, st, "b", "zeta")
	seedEntry(t, st, "a", "alpha")
	seedEntry(t, st, "c", "zeta")
	cats, err = st.Categories()
	if err != nil || len(cats) != 2 || cats[0] != "alpha" || cats[1] != "zeta" {
		t.Fatalf("分类应去重排序: %v %v", cats, err)
	}
}

func TestTemplateCRUD(t *testing.T) {
	st := tplSchema(t)

	// 内置模板
	builtin := &model.Template{
		Category: "docker_registry", Name: "Docker 镜像仓库", Group: "容器镜像",
		Fields:   []model.Field{{Key: "password", Type: "text", IsSecret: true, Value: "不应存储"}},
	}
	if err := st.SaveTemplate(builtin); err != nil {
		t.Fatal(err)
	}
	if builtin.ID == 0 {
		t.Fatal("保存后应有 ID")
	}
	// 值被清空
	got, err := st.GetTemplate(builtin.ID)
	if err != nil || got.Builtin != false {
		t.Fatalf("GetTemplate 失败: %v %+v", err, got)
	}
	for _, f := range got.Fields {
		if f.Value != "" {
			t.Fatalf("模板字段值应清空: %+v", f)
		}
	}

	// 空名拒绝
	if err := st.SaveTemplate(&model.Template{Name: "  ", Category: "x"}); err == nil {
		t.Fatal("空模板名应报错")
	}
	// 默认 category / group 填充
	t2 := &model.Template{Name: "自动分类"}
	if err := st.SaveTemplate(t2); err != nil {
		t.Fatal(err)
	}
	if t2.Category == "" || t2.Group != "我的模板" {
		t.Fatalf("默认值填充错误: %+v", t2)
	}
	// 同 category 唯一约束（UNIQUE）
	if err := st.SaveTemplate(&model.Template{Name: "撞分类", Category: "docker_registry"}); err == nil {
		t.Fatal("重复 category 应报错")
	}

	// 更新已有模板（ID>0 走 UPDATE）
	builtin.Name = "Docker 仓库 v2"
	if err := st.SaveTemplate(builtin); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTemplate(builtin.ID)
	if got.Name != "Docker 仓库 v2" {
		t.Fatalf("更新未生效: %s", got.Name)
	}
	// 更新不存在的 ID
	ghost := &model.Template{ID: 999, Name: "幽灵", Category: "ghost"}
	if err := st.SaveTemplate(ghost); err == nil {
		t.Fatal("更新不存在模板应报错")
	}

	// 列表
	list, err := st.ListTemplates()
	if err != nil || len(list) != 2 {
		t.Fatalf("模板列表应 2 条: %v %d", err, len(list))
	}
	// GetTemplate 不存在
	if _, err := st.GetTemplate(999); err == nil {
		t.Fatal("不存在模板应报错")
	}
	// 删除
	if err := st.DeleteTemplate(t2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTemplate(t2.ID); err == nil {
		t.Fatal("删除后应查不到")
	}
}

// RotateAllEntries 空库也成功（0 条，key_check 仍更新）
func TestRotateEmpty(t *testing.T) {
	st := newTestStore(t)
	raw := make([]byte, 32)
	rand.Read(raw)
	newCipher, err := crypto.New(crypto.DeriveKeyText(base64.StdEncoding.EncodeToString(raw)))
	if err != nil {
		t.Fatal(err)
	}
	n, err := st.RotateAllEntries(newCipher)
	if err != nil || n != 0 {
		t.Fatalf("空库轮换应 0 条无错: n=%d err=%v", n, err)
	}
	var kc string
	st.DB.QueryRow(`SELECT value FROM settings WHERE key = 'key_check'`).Scan(&kc)
	if !newCipher.VerifyKeyCheck(kc) {
		t.Fatal("空库轮换 key_check 也应更新")
	}
}

// NewForTest 失败路径难以触发（rand.Read 失败），主路径已由全部用例覆盖
func TestStoreCipherSwap(t *testing.T) {
	st := newTestStore(t)
	old := st.Cipher
	// cipher() 读取（RWMutex 路径）
	if st.cipher() != old {
		t.Fatal("cipher 应返回当前密钥")
	}
	st.mu.Lock()
	st.Cipher = old
	st.mu.Unlock()
}

// 损坏数据与 DB 故障路径
func TestCorruptAndClosedDB(t *testing.T) {
	st := newTestStore(t)
	e := seedEntry(t, st, "好数据", "misc",
		model.Field{Key: "pw", Type: "text", IsSecret: true, Value: "v"})

	// 坏 JSON
	st.DB.Exec(`UPDATE entries SET fields = 'not-json' WHERE id = ?`, e.ID)
	if _, err := st.GetEntry(e.ID); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
	// 合法 JSON 但坏密文
	st.DB.Exec(`UPDATE entries SET fields = ? WHERE id = ?`,
		`[{"key":"pw","type":"text","is_secret":true,"value":"enc:v1:$$$不是base64$$$"}]`, e.ID)
	if _, err := st.GetEntry(e.ID); err == nil {
		t.Fatal("坏密文应报错")
	}
	if _, err := st.ListEntries("好", ""); err == nil {
		t.Fatal("列表扫描坏数据应报错")
	}
	// 坏 tags JSON（忽略错误，tags 为空）
	st.DB.Exec(`UPDATE entries SET tags = 'bad' WHERE id = ?`, e.ID)

	// 关库后：各写路径应报错
	st.DB.Close()
	if _, err := st.ListEntries("", ""); err == nil {
		t.Fatal("关库 ListEntries 应报错")
	}
	if _, err := st.Categories(); err == nil {
		t.Fatal("关库 Categories 应报错")
	}
	ne := model.Entry{Title: "x", Fields: nil}
	if err := st.CreateEntry(&ne); err == nil {
		t.Fatal("关库 CreateEntry 应报错")
	}
	if err := st.DeleteEntry(1); err == nil {
		t.Fatal("关库 DeleteEntry 应报错")
	}
}

// RotateAllEntries 遇坏数据/关库应报错且回滚（旧密钥不变）
func TestRotateErrorPaths(t *testing.T) {
	st := newTestStore(t)
	seedEntry(t, st, "坏数据", "misc", model.Field{Key: "p", Type: "text", IsSecret: true, Value: "v"})
	st.DB.Exec(`UPDATE entries SET fields = 'garbage'`)
	newCipher, _ := crypto.New(make([]byte, 32))
	if _, err := st.RotateAllEntries(newCipher); err == nil {
		t.Fatal("坏数据轮换应报错")
	}
	// 坏密文
	st.DB.Exec(`UPDATE entries SET fields = ?`,
		`[{"key":"p","is_secret":true,"value":"enc:v1:###"}]`)
	if _, err := st.RotateAllEntries(newCipher); err == nil {
		t.Fatal("坏密文轮换应报错")
	}
	// 关库：查询失败
	st.DB.Close()
	if _, err := st.RotateAllEntries(newCipher); err == nil {
		t.Fatal("关库轮换应报错")
	}
}
