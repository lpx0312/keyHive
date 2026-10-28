package auth

// 本文件为代码审查后的边界补测 + 缺陷复现测试：
//   - 普通测试为边界补测（锚定当前 fail-closed 行为），应绿；
//   - TestBug_ 前缀的用例为缺陷复现（预期红）：断言"正确行为"，生产代码修复前会失败。
//
// 对应报告：docs/review/01-review-auth.md（P3 畸形 argon2 参数 panic、P3 Bearer 大小写/多空格）

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---- 盲区补测（应绿，锚定当前 fail-closed 行为）----

// Bearer 解析边界：小写 scheme、双空格、无令牌、尾随空格等当前全部 fail closed（401），
// 合法形态放行。一旦解析逻辑被调整（如按 RFC 6750 大小写不敏感），本用例是回归锚点。
func TestBearerSchemeEdges(t *testing.T) {
	db := newDB(t)
	_, plain, _ := CreateAPIToken(db, "edge", []string{"read"}, nil)

	probe := func(authHeader string) int {
		mw := RequireToken(db, "") // 空 scope：只考解析与有效性
		code := 0
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
		r := httptest.NewRequest("GET", "/x", nil)
		if authHeader != "" {
			r.Header.Set("Authorization", authHeader)
		}
		w := httptest.NewRecorder()
		mw(next).ServeHTTP(w, r)
		code = w.Code
		return code
	}

	// 合法形态（对照组）应放行
	if code := probe("Bearer " + plain); code != 200 {
		t.Fatalf("标准 Bearer 应放行，实际 %d", code)
	}
	// 边界形态：当前实现一律 fail closed → 401（不得 200/500/panic）
	for _, h := range []string{
		"bearer " + plain,  // 小写 scheme（RFC 6750 应不敏感，当前拒绝）
		"BEARER " + plain,  // 全大写 scheme
		"Bearer",           // 无空格无令牌
		"Bearer ",          // 空令牌
		"Bearer  " + plain, // 双空格 → 令牌带前导空格
		"Bearer " + plain + " ", // 尾随空格 → 令牌带尾部空格
		" Bearer " + plain,      // 前导空格
		"Token " + plain,        // 其他 scheme
	} {
		if code := probe(h); code != 401 {
			t.Errorf("Authorization %q 当前应 fail closed 401，实际 %d", h, code)
		}
	}
}

// 滑动续期应真实前移 expires_at（而非只更新 last_seen）：
// 把有效期改到 6 天后（仍有效），一次 GetSession 后应回到 ~now+7d
func TestSessionSlidingRenewalMovesExpiry(t *testing.T) {
	db := newDB(t)
	token, rec, err := CreateSession(db, 9, "1.1.1.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟"用过 1 天"的会话：expires_at 还剩 6 天
	soon := time.Now().UTC().Add(6 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`, soon, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetSession(db, token); err != nil {
		t.Fatal(err)
	}
	var expStr string
	db.QueryRow(`SELECT expires_at FROM sessions WHERE id = ?`, rec.ID).Scan(&expStr)
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil {
		t.Fatalf("expires_at 非法: %q", expStr)
	}
	// 续期后应至少在 6 天 + 1 小时之外（即已前移到 ~7 天）
	if exp.Before(time.Now().UTC().Add(6*24*time.Hour + time.Hour)) {
		t.Fatalf("滑动续期应把 expires_at 前移到 ~7 天后，实际 %v", expStr)
	}
}

// GetSession 对库内畸形时间串 fail closed（既有行为锚点，防回归）
func TestSessionRenewKeepsFailClosedOnGarbage(t *testing.T) {
	db := newDB(t)
	token, rec, _ := CreateSession(db, 3, "", "")
	// created_at 为垃圾串不影响过期判断路径本身；expires_at 垃圾串必须拒绝
	db.Exec(`UPDATE sessions SET expires_at = 'not-a-time' WHERE id = ?`, rec.ID)
	if _, err := GetSession(db, token); err != ErrSessionInvalid {
		t.Fatalf("垃圾 expires_at 应 ErrSessionInvalid，实际 %v", err)
	}
}

// ---- 缺陷复现（预期红）----

// verifySafe 执行 VerifyPassword 并捕获 panic：断言"返回 false 且不 panic"用
func verifySafe(password, encoded string) (result bool, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	result = VerifyPassword(password, encoded)
	return result, false
}

// 【P3，预期红】password.go:59 —— VerifyPassword 解析 PHC 串后未校验参数取值，
// 对畸形串（t=0 / p=0 / 空 hash 段等）panic（或空 hash 段长度 0 恒等比较）而非返回 false。
// 正确行为：任何畸形哈希都应安全返回 false，绝不 panic、绝不返回 true。
func TestBug_VerifyPasswordMalformedNoPanic(t *testing.T) {
	// 盐/哈希段用合法 base64（"AAAAAAAAAAAAAAAA"），确保能走到 argon2.IDKey 调用
	const (
		saltB64 = "AAAAAAAAAAAAAAAA"
		hashB64 = "AAAAAAAAAAAAAAAA"
	)
	cases := []struct{ name, encoded string }{
		{"t=0（轮数为 0）", "$argon2id$v=19$m=65536,t=0,p=4$" + saltB64 + "$" + hashB64},
		{"p=0（并行度为 0）", "$argon2id$v=19$m=65536,t=1,p=0$" + saltB64 + "$" + hashB64},
		{"m=0（内存为 0，被库静默 clamp）", "$argon2id$v=19$m=0,t=1,p=4$" + saltB64 + "$" + hashB64},
		{"空 hash 段", "$argon2id$v=19$m=65536,t=1,p=4$" + saltB64 + "$"},
		{"空盐 + 空 hash 段", "$argon2id$v=19$m=65536,t=1,p=4$$"},
		{"缺 hash 段（5 段）", "$argon2id$v=19$m=65536,t=1,p=4$" + saltB64},
		{"非 argon2 算法名", "$argon2i$v=19$m=65536,t=1,p=4$" + saltB64 + "$" + hashB64},
		{"bcrypt 风格串", "$2b$12$abcdefghijklmnopqrstuv"},
		{"空串", ""},
		{"纯文本", "plaintext-password-hash"},
	}
	for _, c := range cases {
		got, panicked := verifySafe("some-password", c.encoded)
		if panicked {
			t.Errorf("[P3] VerifyPassword(%q) 不应 panic（应返回 false）", c.name)
			continue
		}
		if got {
			t.Errorf("[P3] VerifyPassword(%q) 畸形哈希不应验证通过（应返回 false）", c.name)
		}
	}
	// 同一畸形串也不应因密码不同而 panic
	if _, panicked := verifySafe("", "$argon2id$v=19$m=65536,t=1,p=0$"+saltB64+"$"+hashB64); panicked {
		t.Error("[P3] 空密码 + 畸形哈希不应 panic")
	}
}
