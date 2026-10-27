package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"


	"github.com/lpx0312/keyHive/internal/auth"
	"github.com/lpx0312/keyHive/internal/crypto"
	kdb "github.com/lpx0312/keyHive/internal/db"
	"github.com/lpx0312/keyHive/web"
)

// e2eEnv 完整复刻 runServe 的组装（真实文件库 + 主密钥文件 + embed 前端）
type e2eEnv struct {
	srv     *httptest.Server
	dataDir string
	db      *sql.DB
}

func (e *e2eEnv) URL() string { return e.srv.URL }

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", "")
	dataDir := t.TempDir()

	// buildServer 是 runServe 的可测形态：主密钥 + 库 + 全部路由一次组装
	srvObj, database, err := buildServer(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() }) // Windows 下必须先关库句柄才能删 TempDir
	srv := startServer(t, srvObj.Handler)
	return &e2eEnv{srv: srv, dataDir: dataDir, db: database}
}

// startServer 起真实 HTTP 服务（httptest 监听 127.0.0.1 随机端口）
func startServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// ---- e2e 全流程：登录 → 条目 CRUD → 遮蔽/reveal → TOTP → 模板 → AI 令牌 → AI API → 导出导入 → 轮换 → 审计 ----

func TestE2EFullJourney(t *testing.T) {
	env := newE2E(t)
	base := env.srv.URL + "/api/v1"

	// 首启后把 admin 密码设为已知值（等价管理员初始化）
	hash, _ := auth.HashPassword("e2e-admin-pw")
	env.db.Exec(`UPDATE users SET password_hash = ? WHERE username = 'admin'`, hash)

	admin := newClient(t)
	// 登录
	if code, body := httpDo(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"e2e-admin-pw"}`); code != 200 {
		t.Fatalf("登录失败: %d %s", code, body)
	}

	// 建条目（含敏感字段 + TOTP）
	entry := `{"title":"生产ACR","category":"docker_registry","description":"e2e","ai_visible":true,
	  "tags":["生产"],
	  "fields":[{"key":"username","description":"用户名","type":"text","is_secret":false,"value":"lipanx"},
	            {"key":"password","description":"密码","type":"text","is_secret":true,"value":"e2e-pw-123"},
	            {"key":"totp_secret","description":"两步验证 TOTP","type":"text","is_secret":true,"value":"JBSWY3DPEHPK3PXP"}]}`
	if code, body := httpDo(t, admin, "POST", base+"/entries", entry); code != 201 {
		t.Fatalf("建条目失败: %d %s", code, body)
	}

	// 列表：遮蔽
	code, body := httpDo(t, admin, "GET", base+"/entries?q=生产ACR", "")
	if code != 200 || !strings.Contains(body, `"***"`) || strings.Contains(body, "e2e-pw-123") {
		t.Fatalf("列表应遮蔽敏感值: %d %s", code, body)
	}
	// 单条 reveal：明文
	code, body = httpDo(t, admin, "GET", base+"/entries/1?reveal=true", "")
	if code != 200 || !strings.Contains(body, "e2e-pw-123") {
		t.Fatalf("reveal 应返回明文: %d %s", code, body)
	}
	// TOTP
	code, body = httpDo(t, admin, "POST", base+"/entries/1/totp", `{}`)
	if code != 200 || !strings.Contains(body, `"code"`) {
		t.Fatalf("TOTP 失败: %d %s", code, body)
	}

	// 模板：另存 + 列表
	if code, _ := httpDo(t, admin, "POST", base+"/entries/1/save-as-template", `{"name":"e2e模板"}`); code != 201 {
		t.Fatalf("另存模板失败: %d", code)
	}

	// AI 令牌（reveal 级）
	code, body = httpDo(t, admin, "POST", base+"/tokens", `{"name":"e2e-token","scopes":["read","search","reveal"]}`)
	if code != 201 {
		t.Fatalf("建令牌失败: %d %s", code, body)
	}
	var tokResp struct {
		Token string `json:"token"`
	}
	json.Unmarshal([]byte(body), &tokResp)
	if tokResp.Token == "" {
		t.Fatal("令牌明文缺失")
	}

	// AI API（Bearer）：列表遮蔽 + search + reveal 明文
	aiBase := env.srv.URL + "/api/v1/ai"
	req, _ := http.NewRequest("GET", aiBase+"/entries", nil)
	req.Header.Set("Authorization", "Bearer "+tokResp.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("AI 列表失败: %v %v", err, resp)
	}
	aiList, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(aiList), `"***"`) || strings.Contains(string(aiList), "e2e-pw-123") {
		t.Fatalf("AI 列表应遮蔽: %s", aiList)
	}
	req, _ = http.NewRequest("POST", aiBase+"/entries/1/reveal", strings.NewReader(`{"field":"password"}`))
	req.Header.Set("Authorization", "Bearer "+tokResp.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("AI reveal 失败: %v", err)
	}
	rv, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(rv), "e2e-pw-123") {
		t.Fatalf("AI reveal 应明文: %s", rv)
	}
	// 无令牌 401
	resp2, _ := http.Get(aiBase + "/entries")
	if resp2.StatusCode != 401 {
		t.Fatalf("AI 无令牌应 401，实际 %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	// 导出 → 导入 round-trip
	code, exported := httpGet(t, admin, base+"/export")
	if code != 200 || !strings.Contains(exported, "e2e-pw-123") {
		t.Fatalf("导出应含明文: %d", code)
	}
	csvJSON, _ := json.Marshal(exported)
	if code, body := httpDo(t, admin, "POST", base+"/import",
		`{"format":"keyhive","csv":`+string(csvJSON)+`,"dry_run":false}`); code != 200 {
		t.Fatalf("导入失败: %d %s", code, body)
	}

	// 密钥轮换：文件模式，密钥落盘 + 数据仍可解密
	if code, body := httpDo(t, admin, "POST", base+"/rotate-key", ""); code != 200 {
		t.Fatalf("轮换失败: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(env.dataDir, "master.key")); err != nil {
		t.Fatalf("新密钥应落盘: %v", err)
	}
	code, body = httpDo(t, admin, "GET", base+"/entries/1?reveal=true", "")
	if code != 200 || !strings.Contains(body, "e2e-pw-123") {
		t.Fatalf("轮换后应仍可解密: %d %s", code, body)
	}

	// 审计：关键动作全有
	for _, action := range []string{"login", "entry_create", "entry_reveal", "totp_gen", "template_save",
		"token_create", "field_reveal", "export", "import", "key_rotate"} {
		var n int
		env.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, action).Scan(&n)
		if n == 0 {
			t.Errorf("审计缺少动作 %s", action)
		}
	}

	// 改密 → 旧会话失效
	if code, _ := httpDo(t, admin, "POST", base+"/password", `{"old_password":"e2e-admin-pw","new_password":"e2e-new-pw-456"}`); code != 200 {
		t.Fatal("改密失败")
	}
	if code, _ := httpDo(t, admin, "GET", base+"/auth/me", ""); code != 401 {
		t.Fatal("改密后旧会话应 401")
	}
	if code, _ := httpDo(t, newClient(t), "POST", base+"/auth/login", `{"username":"admin","password":"e2e-new-pw-456"}`); code != 200 {
		t.Fatal("新密码应可登录")
	}
}

// SPA 静态资源：index 回退 + 缓存头
func TestE2EStaticAssets(t *testing.T) {
	env := newE2E(t)
	// 首页
	resp, err := http.Get(env.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<!DOCTYPE html") && !strings.Contains(string(body), "<html") {
		t.Fatalf("首页异常: %d %.60s", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index 应 no-cache: %q", cc)
	}
	// 前端路由路径回退 index.html
	resp2, _ := http.Get(env.srv.URL + "/entries")
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 200 || len(b2) == 0 {
		t.Fatalf("SPA 回退异常: %d", resp2.StatusCode)
	}
	// API 404 保持 JSON 错误
	resp3, _ := http.Get(env.srv.URL + "/api/v1/no-such")
	resp3.Body.Close()
	if resp3.StatusCode != 404 {
		t.Fatalf("API 未知路径应 404，实际 %d", resp3.StatusCode)
	}
}

// ---- 单元：ensureKeyCheck / spaHandler / envOr ----

func TestEnsureKeyCheck(t *testing.T) {
	d, err := kdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	c1, _ := crypto.New(crypto.DeriveKeyText("key-one"))
	// 首次写入
	if err := ensureKeyCheck(d, c1); err != nil {
		t.Fatal(err)
	}
	// 同密钥校验通过
	if err := ensureKeyCheck(d, c1); err != nil {
		t.Fatal(err)
	}
	// 换密钥 → 不匹配报错
	c2, _ := crypto.New(crypto.DeriveKeyText("key-two"))
	if err := ensureKeyCheck(d, c2); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("换密钥应报不匹配: %v", err)
	}
}

func TestSPAHandlerDirect(t *testing.T) {
	dist, _ := fs.Sub(web.Dist, "dist")
	// 找一个真实存在的 assets 文件验证长缓存头
	matches, _ := fs.Glob(dist, "assets/*.js")
	if len(matches) == 0 {
		t.Fatal("dist 中应有 assets 产物")
	}
	h := spaHandler(dist)
	req, _ := http.NewRequest("GET", "/"+matches[0], nil)
	w := newRecorder()
	h.ServeHTTP(w, req)
	if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("assets 缓存头错误: %q", w.Header().Get("Cache-Control"))
	}
	if w.code != 200 {
		t.Fatalf("assets 应可访问: %d", w.code)
	}
	// 根路径 no-cache
	req2, _ := http.NewRequest("GET", "/", nil)
	w2 := newRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("根路径应 no-cache: %q", w2.Header().Get("Cache-Control"))
	}
	// 不存在的 assets（回退 index，no-cache）
	req3, _ := http.NewRequest("GET", "/assets/whatever.js", nil)
	w3 := newRecorder()
	h.ServeHTTP(w3, req3)
	if w3.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("不存在 assets 应回退 no-cache: %q", w3.Header().Get("Cache-Control"))
	}
}

func TestEnvOr(t *testing.T) {
	if envOr("X", "d") != "d" {
		t.Fatal("默认值错误")
	}
	t.Setenv("X", " v ")
	if envOr("X", "d") != "v" {
		t.Fatal("环境变量应 trim")
	}
}

// ---- 测试辅助 ----

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func httpDo(t *testing.T, c *http.Client, method, url, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func httpGet(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	return httpDo(t, c, "GET", url, "")
}

func newRecorder() *respRecorder { return &respRecorder{h: http.Header{}} }

type respRecorder struct {
	h      http.Header
	code   int
	body   strings.Builder
}

func (r *respRecorder) Header() http.Header        { return r.h }
func (r *respRecorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = 200
	}
	return r.body.Write(b)
}
func (r *respRecorder) WriteHeader(code int) { r.code = code }


func TestBuildServerErrorPaths(t *testing.T) {
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", "")
	// key_check 不匹配：先用目录 A 生成密钥，再篡改其 master.key 后重建
	dir := t.TempDir()
	if _, db1, err := buildServer(dir); err != nil {
		t.Fatal(err)
	} else {
		db1.Close()
	}
	os.WriteFile(filepath.Join(dir, "master.key"), []byte("tampered-key\n"), 0o600)
	if _, _, err := buildServer(dir); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("密钥篡改后应报不匹配: %v", err)
	}
	// 库目录不可写
	bad := filepath.Join(dir, "master.key", "sub") // master.key 已是文件，路径必然失败
	if _, _, err := buildServer(bad); err == nil {
		t.Fatal("非法数据目录应报错")
	}
}
