package audit

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor_type TEXT NOT NULL, actor_id INTEGER NOT NULL DEFAULT 0, actor_name TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL, entry_id INTEGER, detail TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLogAndList(t *testing.T) {
	db := newDB(t)

	// 空 detail 补 {}
	id1 := int64(101)
	Log(db, "user", 1, "admin", "entry_create", &id1, "", "1.1.1.1")
	Log(db, "user", 1, "admin", "entry_delete", nil, `{"x":1}`, "1.1.1.1")
	Log(db, "token", 2, "cli", "entry_create", nil, "", "")

	// 全量倒序
	list, err := List(db, "", 50, 0)
	if err != nil || len(list) != 3 {
		t.Fatalf("应 3 条: %v %d", err, len(list))
	}
	if list[0].Action != "entry_create" || list[0].ActorType != "token" {
		t.Fatalf("应按 id 倒序: %+v", list[0])
	}
	// 空 detail 已补 {}
	if list[2].Detail != "{}" {
		t.Fatalf("空 detail 应补 {}: %q", list[2].Detail)
	}
	// entry_id 指针
	if list[0].EntryID != nil {
		t.Fatalf("无 entry_id 应为 nil: %v", list[0].EntryID)
	}
	if list[2].EntryID == nil || *list[2].EntryID != 101 {
		t.Fatalf("entry_id 应还原 101: %v", list[2].EntryID)
	}

	// action 筛选
	list, _ = List(db, "entry_create", 50, 0)
	if len(list) != 2 {
		t.Fatalf("筛选应 2 条: %d", len(list))
	}
	// 分页
	list, _ = List(db, "", 2, 1)
	if len(list) != 2 {
		t.Fatalf("limit=2 offset=1 应 2 条: %d", len(list))
	}
	if list[0].ID != 2 {
		t.Fatalf("offset=1 应从第 2 条开始: %+v", list[0])
	}
}

func TestListLimitClamp(t *testing.T) {
	db := newDB(t)
	for i := 0; i < 3; i++ {
		Log(db, "user", 1, "u", "login", nil, "", "")
	}
	// limit<=0 与 >200 都收敛到 50
	for _, lim := range []int{0, -1, 201, 999} {
		list, err := List(db, "", lim, 0)
		if err != nil || len(list) != 3 {
			t.Fatalf("limit=%d 应按 50 处理返回 3 条: %v %d", lim, err, len(list))
		}
	}
}

func TestCount(t *testing.T) {
	db := newDB(t)
	Log(db, "user", 1, "u", "login", nil, "", "")
	Log(db, "user", 1, "u", "login", nil, "", "")
	Log(db, "user", 1, "u", "export", nil, "", "")

	if n, err := Count(db, ""); err != nil || n != 3 {
		t.Fatalf("总数应 3: %v %d", err, n)
	}
	if n, _ := Count(db, "login"); n != 2 {
		t.Fatalf("login 应 2: %d", n)
	}
	if n, _ := Count(db, "不存在"); n != 0 {
		t.Fatalf("无匹配应 0: %d", n)
	}
}
