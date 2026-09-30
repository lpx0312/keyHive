package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"keyhive/internal/crypto"
	"keyhive/internal/model"
)

// testDB 每次全新内存库（含表结构，跳过种子）
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	for _, s := range testSchema {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	return d
}

var testSchema = []string{
	`CREATE TABLE entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		category TEXT NOT NULL DEFAULT 'misc',
		description TEXT NOT NULL DEFAULT '',
		fields TEXT NOT NULL DEFAULT '[]',
		ai_visible INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := NewForTest(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSecretsEncryptedAtRest(t *testing.T) {
	st := newTestStore(t)
	e := model.Entry{
		Title: "SWR", Category: "huawei_swr",
		Fields: []model.Field{
			{Key: "org", Type: "text", Value: "prod"},
			{Key: "password", Type: "text", IsSecret: true, Value: "明文密码123"},
		},
	}
	if err := st.CreateEntry(&e); err != nil {
		t.Fatal(err)
	}

	// 直接查库：敏感值必须是密文，非敏感值明文
	var raw string
	st.DB.QueryRow(`SELECT fields FROM entries WHERE id = ?`, e.ID).Scan(&raw)
	if !crypto.IsEncrypted(extractValue(t, raw, "password")) {
		t.Fatal("敏感字段未加密落库")
	}
	if extractValue(t, raw, "org") != "prod" {
		t.Fatal("非敏感字段应明文存储")
	}

	// 读回：解密正确
	got, err := st.GetEntry(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FieldByKey("password").Value != "明文密码123" {
		t.Fatal("读回解密失败")
	}
}

func TestUpdateMaskedSentinelKeepsOldValue(t *testing.T) {
	st := newTestStore(t)
	e := model.Entry{
		Title: "DB", Category: "mysql",
		Fields: []model.Field{
			{Key: "password", Type: "text", IsSecret: true, Value: "旧密码"},
		},
	}
	st.CreateEntry(&e)

	// 前端未改密码时回传 ***
	upd := model.Entry{
		Title: "DB-改名", Category: "mysql",
		Fields: []model.Field{
			{Key: "password", Type: "text", IsSecret: true, Value: model.MaskedValue},
		},
	}
	if err := st.UpdateEntry(e.ID, &upd); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetEntry(e.ID)
	if got.FieldByKey("password").Value != "旧密码" {
		t.Fatalf("*** 回传应保留原值，实际 %q", got.FieldByKey("password").Value)
	}
	if got.Title != "DB-改名" {
		t.Fatal("标题应更新")
	}
}

func TestValidateRejects(t *testing.T) {
	st := newTestStore(t)
	cases := []model.Entry{
		{Title: "  ", Fields: nil},                                            // 空标题
		{Title: "x", Fields: []model.Field{{Key: "a"}, {Key: "a"}}},          // 重复字段
		{Title: "x", Fields: []model.Field{{Key: " ", Value: "1"}}},          // 空字段名
		{Title: "x", Fields: []model.Field{{Key: "a", Type: "bad"}}},         // 非法类型
	}
	for i, e := range cases {
		if err := st.CreateEntry(&e); err == nil {
			t.Fatalf("case %d 应被拒绝", i)
		}
	}
}

func extractValue(t *testing.T, fieldsJSON, key string) string {
	t.Helper()
	var fields []model.Field
	if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}
