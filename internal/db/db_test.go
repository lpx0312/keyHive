package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// 旧库（v1.3.0 及以前，entries 无 tags 列）升级后应幂等补列，存量行默认 '[]'
func TestMigrateEntriesTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyhive.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 v1.3.0 的旧表结构
	_, err = old.Exec(`CREATE TABLE IF NOT EXISTS entries (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		title       TEXT NOT NULL,
		category    TEXT NOT NULL DEFAULT 'misc',
		description TEXT NOT NULL DEFAULT '',
		fields      TEXT NOT NULL DEFAULT '[]',
		ai_visible  INTEGER NOT NULL DEFAULT 1,
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO entries (title, category, created_at, updated_at)
		VALUES ('存量条目', 'misc', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	opened, err := Open(path)
	if err != nil {
		t.Fatalf("旧库 Open 应成功: %v", err)
	}
	opened.Close()

	up, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	var tags string
	if err := up.QueryRow(`SELECT tags FROM entries WHERE title = '存量条目'`).Scan(&tags); err != nil {
		t.Fatalf("tags 列应已存在: %v", err)
	}
	if tags != "[]" {
		t.Fatalf("存量行 tags 应默认 []，实际 %q", tags)
	}
}
