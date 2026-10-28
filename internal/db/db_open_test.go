package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 同一库文件反复 Open（迁移 + seed 全链路幂等）：不报错、admin/模板/表数量均不增长
func TestOpenRepeatedIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kh.db")

	d1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var users, templates, settings, tables int
	d1.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	d1.QueryRow(`SELECT COUNT(*) FROM category_templates`).Scan(&templates)
	d1.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&settings)
	d1.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&tables)
	if users != 1 || templates < 10 || settings != 1 || tables < 7 {
		t.Fatalf("首次 Open 基线异常: users=%d templates=%d settings=%d tables=%d", users, templates, settings, tables)
	}
	d1.Close()

	// 二次、三次 Open：CREATE TABLE IF NOT EXISTS + seeded 标记防重
	for i := 2; i <= 3; i++ {
		d, err := Open(path)
		if err != nil {
			t.Fatalf("第 %d 次 Open 不应报错: %v", i, err)
		}
		var u, tpl, st, tb int
		d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&u)
		d.QueryRow(`SELECT COUNT(*) FROM category_templates`).Scan(&tpl)
		d.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&st)
		d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&tb)
		d.Close()
		if u != users || tpl != templates || st != settings || tb != tables {
			t.Fatalf("第 %d 次 Open 后数据增长: users=%d/%d templates=%d/%d settings=%d/%d tables=%d/%d",
				i, u, users, tpl, templates, st, settings, tb, tables)
		}
		// seeded 标记只有一行，admin 仍恰 1 个
		var seeded, admin int
		d2, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		d2.QueryRow(`SELECT COUNT(*) FROM settings WHERE key='seeded'`).Scan(&seeded)
		d2.QueryRow(`SELECT COUNT(*) FROM users WHERE username='admin' AND is_admin=1`).Scan(&admin)
		d2.Close()
		if seeded != 1 || admin != 1 {
			t.Fatalf("重复 Open 后 seeded=%d admin=%d，应均为 1", seeded, admin)
		}
	}
}

// seed 后管理员形状完整：argon2id 哈希、启用、非禁用、时间戳合法
func TestSeedAdminShape(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	var hash, createdAt, updatedAt string
	var isAdmin, disabled int
	if err := d.QueryRow(`SELECT password_hash, is_admin, disabled, created_at, updated_at
		FROM users WHERE username='admin'`).Scan(&hash, &isAdmin, &disabled, &createdAt, &updatedAt); err != nil {
		t.Fatalf("admin 应存在: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") || len(hash) < 40 {
		t.Fatalf("admin 密码哈希应为 argon2id 格式: %.30s", hash)
	}
	if isAdmin != 1 || disabled != 0 {
		t.Fatalf("admin 应启用且未禁用: is_admin=%d disabled=%d", isAdmin, disabled)
	}
	for _, ts := range []string{createdAt, updatedAt} {
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Fatalf("时间戳应为 RFC3339: %q %v", ts, err)
		}
	}
	// seeded 标记已写入（非空值）
	var seeded string
	if err := d.QueryRow(`SELECT value FROM settings WHERE key='seeded'`).Scan(&seeded); err != nil || seeded == "" {
		t.Fatalf("seeded 标记缺失: %v %q", err, seeded)
	}
}
