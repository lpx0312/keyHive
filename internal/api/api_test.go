package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"keyhive/internal/kdbtest"
)

const adminPW = "admin-test-PW123"

// newHumanServer 完整人用 API（含 session/admin 中间件，与 main 挂法一致）
func newHumanServer(t *testing.T) (*httptest.Server, *kdbtest.Fixture) {
	t.Helper()
	fx := kdbtest.New(t)
	fx.SetPassword(t, "admin", adminPW)
	r := chi.NewRouter()
	r.Route("/api/v1", (&Server{Store: fx.Store}).Routes)
	return httptest.NewServer(r), fx
}

// client 带独立 cookie jar 的客户端（模拟一个浏览器用户）
func client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func do(t *testing.T, c *http.Client, method, url, body string) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req, _ := http.NewRequest(method, url, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUserManagementLifecycle(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	admin := client(t)
	// admin 登录 + me
	if code, out := do(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`); code != 200 {
		t.Fatalf("admin 登录失败: %d %v", code, out)
	}
	if code, out := do(t, admin, "GET", base+"/auth/me", ""); code != 200 || out["is_admin"] != true {
		t.Fatalf("admin me 应含 is_admin=true: %d %v", code, out)
	}

	// 创建普通用户 alice
	if code, out := do(t, admin, "POST", base+"/users", `{"username":"alice","password":"alice-PW-456","is_admin":false}`); code != 201 {
		t.Fatalf("创建 alice 失败: %d %v", code, out)
	}

	// alice 登录：可用条目，不可用 tokens/audit/users
	alice := client(t)
	if code, out := do(t, alice, "POST", base+"/auth/login", `{"username":"alice","password":"alice-PW-456"}`); code != 200 {
		t.Fatalf("alice 登录失败: %d %v", code, out)
	}
	if code, out := do(t, alice, "GET", base+"/auth/me", ""); code != 200 || out["is_admin"] != false {
		t.Fatalf("alice me 应 is_admin=false: %d %v", code, out)
	}
	if code, _ := do(t, alice, "GET", base+"/entries", ""); code != 200 {
		t.Fatalf("alice 应能访问 entries，实际 %d", code)
	}
	for _, ep := range []string{"/tokens", "/audit", "/users"} {
		if code, out := do(t, alice, "GET", base+ep, ""); code != 403 {
			t.Fatalf("alice 访问 %s 应 403，实际 %d %v", ep, code, out)
		}
	}
	if code, _ := do(t, alice, "POST", base+"/tokens", `{"name":"x","scopes":["read"]}`); code != 403 {
		t.Fatalf("alice 创建令牌应 403，实际 %d", code)
	}

	// 护栏：admin 不能删自己、不能降权自己（唯一管理员）
	if code, _ := do(t, admin, "DELETE", base+"/users/1", ""); code != 400 {
		t.Fatalf("删自己应 400，实际 %d", code)
	}
	if code, _ := do(t, admin, "PUT", base+"/users/1", `{"is_admin":false}`); code != 400 {
		t.Fatalf("降权最后管理员应 400，实际 %d", code)
	}

	// 禁用 alice：旧会话失效，再登录被拒
	if code, _ := do(t, admin, "PUT", base+"/users/2", `{"disabled":true}`); code != 200 {
		t.Fatalf("禁用 alice 失败: %d", code)
	}
	if code, _ := do(t, alice, "GET", base+"/entries", ""); code != 401 {
		t.Fatalf("禁用后旧会话应 401，实际 %d", code)
	}
	if code, out := do(t, alice, "POST", base+"/auth/login", `{"username":"alice","password":"alice-PW-456"}`); code != 403 {
		t.Fatalf("禁用用户登录应 403，实际 %d %v", code, out)
	}

	// 创建第二个管理员；删除 alice；然后自降权成功
	if code, _ := do(t, admin, "POST", base+"/users", `{"username":"bob","password":"bob-admin-789","is_admin":true}`); code != 201 {
		t.Fatalf("创建 bob 失败: %d", code)
	}
	if code, _ := do(t, admin, "DELETE", base+"/users/2", ""); code != 200 {
		t.Fatalf("删除 alice 失败: %d", code)
	}
	if code, _ := do(t, admin, "PUT", base+"/users/1", `{"is_admin":false}`); code != 200 {
		t.Fatalf("存在其他管理员后自降权应成功，实际 %d", code)
	}
	// 自降权后 admin 已无管理权限
	if code, _ := do(t, admin, "GET", base+"/users", ""); code != 403 {
		t.Fatalf("降权后访问 /users 应 403，实际 %d", code)
	}
}

func TestCreateUserValidation(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	admin := client(t)
	do(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	cases := []struct{ body, want string }{
		{`{"username":"","password":"long-enough-pw"}`, "用户名不能为空"},
		{`{"username":"x","password":"short"}`, "密码至少 8 位"},
	}
	for i, c := range cases {
		if code, _ := do(t, admin, "POST", base+"/users", c.body); code != 400 {
			t.Fatalf("case %d 应 400，实际 %d", i, code)
		}
	}
	// 重复用户名
	do(t, admin, "POST", base+"/users", `{"username":"dup","password":"dup-pass-123"}`)
	if code, _ := do(t, admin, "POST", base+"/users", `{"username":"dup","password":"dup-pass-456"}`); code != 400 {
		t.Fatalf("重复用户名应 400，实际 %d", code)
	}
}
