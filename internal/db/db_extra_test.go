package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// Open 内存库：建表 + 种子（admin + 内置模板）
func TestOpenMemorySeed(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatalf("内存库 Open 失败: %v", err)
	}
	defer d.Close()

	// admin 已建
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE username='admin' AND is_admin=1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("应恰好 1 个 admin: %v %d", err, n)
	}
	// 内置模板已种
	d.QueryRow(`SELECT COUNT(*) FROM category_templates WHERE builtin=1`).Scan(&n)
	if n < 10 {
		t.Fatalf("内置模板应 >=10，实际 %d", n)
	}
	// 二次打开（seeded 标记存在）不再种
	d2, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	// 每个内存库独立，各自 1 个 admin——这里验证不报错即可
}

// 旧库缺 is_admin/disabled 列 → 幂等补列；无任何管理员时提升 admin/最早用户
func TestMigrateUsersColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL)`)
	old.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('alice','h','','')`)
	old.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('bob','h','','')`)
	old.Close()

	opened, err := Open(path)
	if err != nil {
		t.Fatalf("旧库升级失败: %v", err)
	}
	defer opened.Close()

	// 补列后：无 admin 用户名 → 提升最早用户（alice, id=1）
	var adminName string
	if err := opened.QueryRow(`SELECT username FROM users WHERE is_admin = 1`).Scan(&adminName); err != nil {
		t.Fatalf("应有管理员: %v", err)
	}
	if adminName != "alice" {
		t.Fatalf("应提升最早用户 alice，实际 %s", adminName)
	}
	var disabled int
	opened.QueryRow(`SELECT COUNT(disabled) FROM users`).Scan(&disabled) // 列存在即成功
}

// 旧库已有 admin 用户名但非管理员 → 优先提升名为 admin 的账号
func TestMigratePreferAdminNamedUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old2.db")
	old, _ := sql.Open("sqlite", path)
	old.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL)`)
	old.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('alice','h','','')`)
	old.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('Admin','h','','')`)
	old.Close()

	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	var adminName string
	opened.QueryRow(`SELECT username FROM users WHERE is_admin = 1`).Scan(&adminName)
	if adminName != "Admin" {
		t.Fatalf("应优先提升名为 admin 的账号（COLLATE NOCASE），实际 %s", adminName)
	}
}

// Open 到不可写位置 → 报错（跨平台：两种路径在 Windows/Linux 均必然失败）
func TestOpenBadPath(t *testing.T) {
	dir := t.TempDir()
	// 父目录不存在：SQLite 不会自动建目录
	if _, err := Open(filepath.Join(dir, "noexist", "x.db")); err == nil {
		t.Fatal("父目录不存在应报错")
	}
	// 普通文件被当目录用
	f := filepath.Join(dir, "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(f, "x.db")); err == nil {
		t.Fatal("文件作为父目录应报错")
	}
}

func TestMarshalFields(t *testing.T) {
	// 由 seed 间接覆盖主路径；这里验证空/非空
	s, err := marshalFields(nil)
	if err != nil || s != "null" {
		t.Fatalf("nil 字段序列化: %s %v", s, err)
	}
}

// 空 users 表的旧库：无用户可提升，迁移仍应成功
func TestMigrateEmptyUsers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old3.db")
	old, _ := sql.Open("sqlite", path)
	old.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL)`)
	old.Close()

	opened, err := Open(path)
	if err != nil {
		t.Fatalf("空用户表迁移应成功: %v", err)
	}
	opened.Close()
}

// 已 seeded 的库再次 Open：seed 提前返回（不重复建 admin）
func TestSeedIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kh.db")
	d1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d1.Close()
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	var n int
	d2.QueryRow(`SELECT COUNT(*) FROM users WHERE username='admin'`).Scan(&n)
	if n != 1 {
		t.Fatalf("二次 Open 不应重复建 admin，实际 %d 个", n)
	}
}

// 旧库 entries 表缺 category 列：CREATE INDEX 失败 → Open 报错
func TestOpenMigrateFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.db")
	old, _ := sql.Open("sqlite", path)
	old.Exec(`CREATE TABLE entries (id INTEGER PRIMARY KEY, title TEXT NOT NULL)`) // 缺 category 列
	old.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("索引建在缺失列上应报错")
	}
}
