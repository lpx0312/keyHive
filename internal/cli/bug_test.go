package cli

// ---------------------------------------------------------------------------
// 缺陷复现测试（预期红）：以下用例断言"正确行为"，在当前生产代码下会失败。
// 对应 docs/review/01-review-cli.md 的 P1/P2 发现，生产代码修复后应转绿。
//
//   - TestBug_AdminFlagParseErrorStops  admincli.go:75
//   - TestBug_ImportSingleLogin         admincli.go:184-193
// ---------------------------------------------------------------------------

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestBug_AdminFlagParseErrorStops P2：adminFlags 丢弃 fs.Parse 的返回错误，
// `rotate-key --dry-run`（不存在的 flag）打错 flag 后程序仍继续用默认参数执行
// 真正的主密钥轮换。
// 正确行为：flag 解析失败应返回退出码 2，且不发起任何请求
// （与 cmdAdd/cmdExport/cmdImport 检查 Parse 错误的行为对齐）。
func TestBug_AdminFlagParseErrorStops(t *testing.T) {
	var reqCount atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")

	cfg := &Config{BaseURL: ts.URL}
	rc := cmdRotateKey(cfg, []string{"--dry-run"})

	if n := reqCount.Load(); n != 0 {
		t.Fatalf("BUG admincli.go:75: flag 解析失败不应发起任何请求（登录/轮换都不允许），实际收到 %d 个请求", n)
	}
	if rc != 2 {
		t.Fatalf("BUG admincli.go:75: flag 解析失败应返回退出码 2（用法错误），实际 %d（rotate-key 已带默认参数继续执行）", rc)
	}
}

// TestBug_ImportSingleLogin P1：cmdImport 在循环里逐条调 AddEntry，而每次
// AddEntry → adminCall → adminClient 都重新 POST /api/v1/auth/login。
// N 条导入 = N 次 argon2id 密码验证 + N 个服务端会话 + N 条登录审计。
// 正确行为：一次导入（无论多少条）只登录 1 次，复用同一会话。
func TestBug_ImportSingleLogin(t *testing.T) {
	var logins, adds atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s", Path: "/"})
			w.Write([]byte(`{}`))
		case "/api/v1/entries":
			if r.Method == http.MethodPost {
				adds.Add(1)
				w.WriteHeader(201)
				w.Write([]byte(`{"id":1}`))
				return
			}
			w.WriteHeader(404)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")

	csv := filepath.Join(t.TempDir(), "c.csv")
	os.WriteFile(csv, []byte("name,url,username,password\nA,u1,p1,x1\nB,u2,p2,x2\nC,u3,p3,x3\n"), 0o600)

	cfg := &Config{BaseURL: ts.URL}
	rc := cmdImport(cfg, []string{"--file", csv, "--format", "chrome"})

	if rc != 0 {
		t.Fatalf("3 条导入应全部成功返回 0，实际 %d", rc)
	}
	if n := adds.Load(); n != 3 {
		t.Fatalf("应创建 3 条条目，实际 %d", n)
	}
	if n := logins.Load(); n != 1 {
		t.Fatalf("BUG admincli.go:184-193: 一次导入只应登录 1 次并复用会话，实际登录 %d 次（每条 AddEntry 都重新登录：N 次 argon2id 验证 + N 个会话 + N 条登录审计）", n)
	}
}
