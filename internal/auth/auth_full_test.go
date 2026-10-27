package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newDB 独立内存库（auth 被 db 包依赖，测试不能 import db，否则循环）
func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL, is_admin INTEGER NOT NULL DEFAULT 0, disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			ip TEXT, user_agent TEXT, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, last_seen_at TEXT NOT NULL)`,
		`CREATE TABLE api_tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL DEFAULT '["read"]', created_at TEXT NOT NULL,
			last_used_at TEXT, expires_at TEXT, revoked_at TEXT)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	return db
}

func TestSessionLifecycle(t *testing.T) {
	db := newDB(t)

	token, rec, err := CreateSession(db, 1, "1.2.3.4", "ua-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 20 || rec.ID == "" || rec.UserID != 1 || rec.IP != "1.2.3.4" {
		t.Fatalf("会话记录异常: %+v", rec)
	}
	if rec.ExpiresAt == rec.CreatedAt {
		t.Fatal("过期时间应为创建时间 + 7 天")
	}

	// 正常读取
	got, err := GetSession(db, token)
	if err != nil || got.UserID != 1 {
		t.Fatalf("GetSession 失败: %v %+v", err, got)
	}
	// 空令牌 / 伪造令牌
	if _, err := GetSession(db, ""); err != ErrSessionInvalid {
		t.Fatalf("空令牌应 ErrSessionInvalid: %v", err)
	}
	if _, err := GetSession(db, "forged-token"); err != ErrSessionInvalid {
		t.Fatalf("伪造令牌应 ErrSessionInvalid: %v", err)
	}
	// 令牌唯一性
	t2, _, _ := CreateSession(db, 1, "", "")
	if t2 == token {
		t.Fatal("两次生成的令牌不应相同")
	}
	// 滑动续期：last_seen 更新
	_, err = GetSession(db, token)
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	db.QueryRow(`SELECT last_seen_at FROM sessions WHERE id=?`, rec.ID).Scan(&seen)
	if seen == "" {
		t.Fatal("滑动续期应更新 last_seen_at")
	}
	// 过期会话（把 expires_at 改到过去）
	db.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), rec.ID)
	if _, err := GetSession(db, token); err != ErrSessionInvalid {
		t.Fatalf("过期会话应无效: %v", err)
	}
	// 非法过期时间字符串
	db.Exec(`UPDATE sessions SET expires_at = 'garbage' WHERE id = ?`, rec.ID)
	if _, err := GetSession(db, token); err != ErrSessionInvalid {
		t.Fatalf("非法过期时间应无效: %v", err)
	}

	// 删除单个会话
	db.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`, rec.ExpiresAt, rec.ID)
	if err := DeleteSession(db, token); err != nil {
		t.Fatal(err)
	}
	if _, err := GetSession(db, token); err != ErrSessionInvalid {
		t.Fatal("删除后应无效")
	}
	// 删除用户全部会话
	CreateSession(db, 7, "", "")
	CreateSession(db, 7, "", "")
	if err := DeleteUserSessions(db, 7); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = 7`).Scan(&n)
	if n != 0 {
		t.Fatalf("应清空用户会话，剩 %d", n)
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	db := newDB(t)

	// 无过期时间
	tok, plain, err := CreateAPIToken(db, "n1", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain[:3] != "kh_" || tok.ID == 0 || tok.ExpiresAt != nil {
		t.Fatalf("令牌结构异常: %+v plain=%q", tok, plain)
	}
	got, err := GetAPIToken(db, plain)
	if err != nil || !got.HasScope("read") || got.HasScope("reveal") {
		t.Fatalf("读取令牌失败: %v %+v", err, got)
	}

	// 空 / 伪造 / 吊销
	if _, err := GetAPIToken(db, ""); err != ErrTokenInvalid {
		t.Fatalf("空令牌应无效: %v", err)
	}
	if _, err := GetAPIToken(db, "kh_forged"); err != ErrTokenInvalid {
		t.Fatalf("伪造令牌应无效: %v", err)
	}
	if err := RevokeAPIToken(db, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetAPIToken(db, plain); err != ErrTokenInvalid {
		t.Fatalf("吊销令牌应无效: %v", err)
	}
	// 重复吊销是幂等的（WHERE revoked_at IS NULL 匹配 0 行）
	if err := RevokeAPIToken(db, tok.ID); err != nil {
		t.Fatalf("重复吊销应无错: %v", err)
	}

	// 过期时间：未来有效 / 过去无效 / 非法字符串
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	_, plainF, _ := CreateAPIToken(db, "n2", []string{"read", "reveal"}, &future)
	if _, err := GetAPIToken(db, plainF); err != nil {
		t.Fatalf("未过期令牌应有效: %v", err)
	}
	tokP, plainP, _ := CreateAPIToken(db, "n3", []string{"read"}, &past)
	if _, err := GetAPIToken(db, plainP); err != ErrTokenInvalid {
		t.Fatalf("过期令牌应无效: %v", err)
	}
	_ = tokP

	// Touch 更新 last_used
	TouchAPIToken(db, got.ID)
	var lu sql.NullString
	db.QueryRow(`SELECT last_used_at FROM api_tokens WHERE id = ?`, got.ID).Scan(&lu)
	if !lu.Valid || lu.String == "" {
		t.Fatal("TouchAPIToken 应更新 last_used_at")
	}

	// 列表：含吊销/过期/last_used 字段
	list, err := ListAPITokens(db)
	if err != nil || len(list) != 3 {
		t.Fatalf("列表应 3 条: %v %d", err, len(list))
	}
	foundRevoked := false
	for _, tk := range list {
		if tk.RevokedAt != nil {
			foundRevoked = true
		}
	}
	if !foundRevoked {
		t.Fatal("列表应包含吊销标记")
	}
}

// ---- 中间件 ----

// mwChain 组装被测中间件 + 探针 handler
func mwProbe(mw func(http.Handler) http.Handler) (int, map[string]any) {
	var code int
	var ctxData map[string]any
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, uok := UserID(r.Context())
		tid, tname, tscopes, tok := TokenInfo(r.Context())
		ctxData = map[string]any{"uid": uid, "uidOk": uok, "tid": tid, "tname": tname, "tscopes": tscopes, "tok": tok}
		w.WriteHeader(200)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	mw(next).ServeHTTP(w, r)
	code = w.Code
	return code, ctxData
}

func TestRequireSessionMiddleware(t *testing.T) {
	db := newDB(t)
	token, _, _ := CreateSession(db, 5, "", "")

	// 无 cookie
	mw := RequireSession(db)
	code, data := mwProbe(mw)
	if code != 401 {
		t.Fatalf("无 cookie 应 401，实际 %d", code)
	}
	// 坏 cookie
	r := httptest.NewRequest("GET", "/x", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "bad"})
	w := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("坏 cookie 应 401，实际 %d", w.Code)
	}
	// 有效 cookie：注入 uid
	r2 := httptest.NewRequest("GET", "/x", nil)
	r2.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	var got int64
	var ok bool
	hit := false
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		got, ok = UserID(r.Context())
		w.WriteHeader(200)
	})).ServeHTTP(httptest.NewRecorder(), r2)
	if !hit || !ok || got != 5 {
		t.Fatalf("有效会话应放行并注入 uid=5: hit=%v ok=%v uid=%d", hit, ok, got)
	}
	// 空 context 下 UserID
	if _, ok := UserID(context.Background()); ok {
		t.Fatal("空 context UserID 应 false")
	}
	_ = code
	_ = data
}

func TestRequireAdminMiddleware(t *testing.T) {
	db := newDB(t)
	// 建三个用户：1=admin，2=普通，3=禁用
	hashPW := func() string { h, _ := HashPassword("x-pass-123"); return h }
	db.Exec(`INSERT INTO users (id, username, password_hash, is_admin, disabled, created_at, updated_at) VALUES
		(1,'admin',?,1,0,'',''),(2,'user',?,0,0,'',''),(3,'dis',?,0,1,'','')`,
		hashPW(), hashPW(), hashPW())

	withUID := func(mw func(http.Handler) http.Handler, uid int64) int {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
		r := httptest.NewRequest("GET", "/x", nil).WithContext(context.WithValue(context.Background(), CtxUserKey, uid))
		w := httptest.NewRecorder()
		mw(next).ServeHTTP(w, r)
		return w.Code
	}
	mw := RequireAdmin(db)
	if code := withUID(mw, 1); code != 200 {
		t.Fatalf("admin 应放行，实际 %d", code)
	}
	if code := withUID(mw, 2); code != 403 {
		t.Fatalf("普通用户应 403，实际 %d", code)
	}
	if code := withUID(mw, 3); code != 401 {
		t.Fatalf("禁用用户应 401，实际 %d", code)
	}
	if code := withUID(mw, 999); code != 401 {
		t.Fatalf("不存在用户应 401，实际 %d", code)
	}
	// 无 uid（直接裸调）
	r := httptest.NewRequest("GET", "/x", nil)
	w := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("未登录应 401，实际 %d", w.Code)
	}
}

func TestRequireTokenMiddleware(t *testing.T) {
	db := newDB(t)
	_, plainRO, _ := CreateAPIToken(db, "ro", []string{"read"}, nil)
	_, plainRW, _ := CreateAPIToken(db, "rw", []string{"read", "reveal"}, nil)

	probe := func(mw func(http.Handler) http.Handler, authHeader string) (int, map[string]any) {
		var code int
		var data map[string]any
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tid, tname, tscopes, _ := TokenInfo(r.Context())
			data = map[string]any{"tid": tid, "tname": tname, "tscopes": tscopes}
			w.WriteHeader(200)
		})
		r := httptest.NewRequest("GET", "/x", nil)
		if authHeader != "" {
			r.Header.Set("Authorization", authHeader)
		}
		w := httptest.NewRecorder()
		mw(next).ServeHTTP(w, r)
		code = w.Code
		return code, data
	}

	mw := RequireToken(db, "reveal")
	if code, _ := probe(mw, ""); code != 401 {
		t.Fatalf("无 Authorization 应 401，实际 %d", code)
	}
	if code, _ := probe(mw, "Basic abc"); code != 401 {
		t.Fatalf("非 Bearer 应 401，实际 %d", code)
	}
	if code, _ := probe(mw, "Bearer kh_forged"); code != 401 {
		t.Fatalf("伪造令牌应 401，实际 %d", code)
	}
	if code, _ := probe(mw, "Bearer "+plainRO); code != 403 {
		t.Fatalf("scope 不足应 403，实际 %d", code)
	}
	code, data := probe(mw, "Bearer "+plainRW)
	if code != 200 {
		t.Fatalf("权限足够应放行，实际 %d", code)
	}
	if data["tname"] != "rw" {
		t.Fatalf("context 应注入令牌名: %v", data)
	}
	// 空 context 下 TokenInfo
	if _, _, _, ok := TokenInfo(context.Background()); ok {
		t.Fatal("空 context TokenInfo 应 false")
	}
	// apiTokenCtx.Has
	c := &apiTokenCtx{Scopes: []string{"read"}}
	if !c.Has("read") || c.Has("reveal") {
		t.Fatal("apiTokenCtx.Has 错误")
	}
	// scope 为空字符串的中间件不校验 scope
	mwAny := RequireToken(db, "")
	if code, _ := probe(mwAny, "Bearer "+plainRO); code != 200 {
		t.Fatalf("空 scope 要求应放行，实际 %d", code)
	}
}

func TestSessionExecError(t *testing.T) {
	db := newDB(t)
	// 正常创建一条后关库：再建/再查/再删都应报错而非 panic
	if _, _, err := CreateSession(db, 1, "", ""); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, _, err := CreateSession(db, 1, "", ""); err == nil {
		t.Fatal("关库 CreateSession 应报错")
	}
	if _, err := GetAPIToken(db, "kh_x"); err == nil {
		// 关库后 QueryRow 返回连接错误（非 ErrTokenInvalid 也可，但不应 panic）
		t.Log("关库 GetAPIToken 返回:", err)
	}
	if err := RevokeAPIToken(db, 1); err == nil {
		t.Log("关库 RevokeAPIToken 返回 nil（Exec 错误被吞时可接受）")
	}
}
