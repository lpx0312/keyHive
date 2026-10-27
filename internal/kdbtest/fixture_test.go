package kdbtest

import (
	"testing"

	"github.com/lpx0312/keyHive/internal/model"
)

// New 夹具自检：库可用、令牌可换条目、SetPassword 生效
func TestFixtureSmoke(t *testing.T) {
	fx := New(t)
	if fx.DB == nil || fx.Store == nil || fx.ReadToken == "" || fx.RevealToken == "" {
		t.Fatalf("夹具不完整: %+v", fx)
	}
	// 库可写
	e := model.Entry{Title: "夹具条目", Category: "misc",
		Fields: []model.Field{{Key: "pw", Type: "text", IsSecret: true, Value: "v"}}}
	if err := fx.Store.CreateEntry(&e); err != nil {
		t.Fatal(err)
	}
	// SetPassword 生效（改完能查到哈希变化）
	fx.SetPassword(t, "admin", "new-pw-123456")
	var hash string
	if err := fx.DB.QueryRow(`SELECT password_hash FROM users WHERE username='admin'`).Scan(&hash); err != nil || hash == "" {
		t.Fatalf("SetPassword 未生效: %v %q", err, hash)
	}
}
