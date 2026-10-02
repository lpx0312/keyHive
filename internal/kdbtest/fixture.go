// Package kdbtest 提供集成测试夹具：内存 SQLite + 随机主密钥 + 预建令牌
package kdbtest

import (
	"database/sql"
	"testing"

	"github.com/lpx0312/keyHive/internal/auth"
	kdb "github.com/lpx0312/keyHive/internal/db"
	"github.com/lpx0312/keyHive/internal/store"
)

type Fixture struct {
	DB          *sql.DB
	Store       *store.Store
	ReadToken   string // scopes: read
	RevealToken string // scopes: read, search, reveal
}

func New(t *testing.T) *Fixture {
	t.Helper()
	d, err := kdb.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	st, err := store.NewForTest(d)
	if err != nil {
		t.Fatalf("初始化 store 失败: %v", err)
	}

	_, readTok, err := auth.CreateAPIToken(d, "test-read", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, revealTok, err := auth.CreateAPIToken(d, "test-reveal", []string{"read", "search", "reveal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Fixture{DB: d, Store: st, ReadToken: readTok, RevealToken: revealTok}
}

// SetPassword 直接重置用户密码（测试辅助，绕过 API）
func (f *Fixture) SetPassword(t *testing.T, username, password string) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB.Exec(`UPDATE users SET password_hash = ? WHERE username = ?`, hash, username); err != nil {
		t.Fatal(err)
	}
}
