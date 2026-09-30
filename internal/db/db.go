package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"keyhive/internal/auth"
	"keyhive/internal/templates"
)

// Open 打开 SQLite（WAL、外键、忙等待），并执行迁移与种子
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 并发写有限，串行化执行避免 database is locked
	d.SetMaxOpenConns(1)
	if err := migrate(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return d, nil
}

func migrate(d *sql.DB) error {
	stmts := []string{
	`CREATE TABLE IF NOT EXISTS users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		is_admin      INTEGER NOT NULL DEFAULT 0,
		disabled      INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL
	)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id           TEXT PRIMARY KEY,
			user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			ip           TEXT,
			user_agent   TEXT,
			created_at   TEXT NOT NULL,
			expires_at   TEXT NOT NULL,
			last_seen_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_tokens (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			name         TEXT NOT NULL,
			token_hash   TEXT NOT NULL UNIQUE,
			scopes       TEXT NOT NULL DEFAULT '["read"]',
			created_at   TEXT NOT NULL,
			last_used_at TEXT,
			expires_at   TEXT,
			revoked_at   TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS entries (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			title       TEXT NOT NULL,
			category    TEXT NOT NULL DEFAULT 'misc',
			description TEXT NOT NULL DEFAULT '',
			fields      TEXT NOT NULL DEFAULT '[]',
			ai_visible  INTEGER NOT NULL DEFAULT 1,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_entries_category ON entries(category)`,
		`CREATE TABLE IF NOT EXISTS category_templates (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			category   TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL,
			group_name TEXT NOT NULL DEFAULT '通用',
			fields     TEXT NOT NULL DEFAULT '[]',
			builtin    INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_type TEXT NOT NULL,
			actor_id   INTEGER NOT NULL DEFAULT 0,
			actor_name TEXT NOT NULL DEFAULT '',
			action     TEXT NOT NULL,
			entry_id   INTEGER,
			detail     TEXT NOT NULL DEFAULT '',
			ip         TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("%w\nSQL: %s", err, s)
		}
	}
	if err := migrateUsersColumns(d); err != nil {
		return err
	}
	return seed(d)
}

// migrateUsersColumns 旧库幂等补列（ALTER TABLE ADD COLUMN 不支持 IF NOT EXISTS）
func migrateUsersColumns(d *sql.DB) error {
	existing := map[string]bool{}
	rows, err := d.Query(`pragma table_info(users)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()

	for _, col := range []string{"is_admin", "disabled"} {
		if !existing[col] {
			if _, err := d.Exec(fmt.Sprintf(`ALTER TABLE users ADD COLUMN %s INTEGER NOT NULL DEFAULT 0`, col)); err != nil {
				return fmt.Errorf("补列 %s 失败: %w", col, err)
			}
		}
	}

	// 兜底：任何库至少要有一个管理员（优先 admin 账号，否则提升最早用户）
	var adminCount int
	if err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&adminCount); err != nil {
		return err
	}
	if adminCount == 0 {
		res, err := d.Exec(`UPDATE users SET is_admin = 1 WHERE id = (
			SELECT id FROM users WHERE username = 'admin' COLLATE NOCASE
			UNION ALL SELECT MIN(id) FROM users LIMIT 1)`)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("迁移：已将现有用户提升为管理员")
		}
	}
	return nil
}

// seed 首次启动：创建 admin、写入内置模板
func seed(d *sql.DB) error {
	var seeded string
	err := d.QueryRow(`SELECT value FROM settings WHERE key = 'seeded'`).Scan(&seeded)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}

	// admin 账号 + 随机密码
	pw, err := auth.RandomToken()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	now := nowUTC()
	if _, err := d.Exec(
		`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES ('admin', ?, 1, ?, ?)`,
		hash, now, now); err != nil {
		return err
	}
	log.Printf("========================================")
	log.Printf("  首次启动：已创建管理员账号")
	log.Printf("  用户名: admin")
	log.Printf("  初始密码: %s", pw)
	log.Printf("  请立即登录并修改密码（此密码不再显示）")
	log.Printf("========================================")

	// 内置模板
	for _, t := range templates.Builtin() {
		fieldsJSON, err := marshalFields(t.Fields)
		if err != nil {
			return err
		}
		if _, err := d.Exec(
			`INSERT OR IGNORE INTO category_templates (category, name, group_name, fields, builtin, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 1, ?, ?)`,
			t.Category, t.Name, t.Group, fieldsJSON, now, now); err != nil {
			return err
		}
	}

	_, err = d.Exec(`INSERT INTO settings (key, value) VALUES ('seeded', ?)`, now)
	return err
}
