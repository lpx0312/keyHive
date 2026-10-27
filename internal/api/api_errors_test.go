package api

import (
	"fmt"
	"io"
	"github.com/go-chi/chi/v5"
	"github.com/lpx0312/keyHive/internal/auth"
	"github.com/lpx0312/keyHive/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lpx0312/keyHive/internal/kdbtest"
)

// 直调 handler（认证在中间件层，handler 本身不校验）打 DB 故障分支：
// 关闭数据库连接后所有查库路径都应返回 500 而非 panic
func TestHandler500Paths(t *testing.T) {
	fx := kdbtest.New(t)
	s := &Server{Store: fx.Store}
	fx.DB.Close() // 全部查询失败

	// GET 类：列表查询失败 500；读单条失败按不存在 404；stale-days 读失败回默认值 200
	cases := []struct {
		name string
		call func(w http.ResponseWriter, r *http.Request)
		want int
	}{
		{"listEntries", s.listEntries, 500},
		{"categories", s.categories, 500},
		{"listTemplates", s.listTemplates, 500},
		{"listTokens", s.listTokens, 500},
		{"listAudit", s.listAudit, 500},
		{"exportEntries", s.exportEntries, 500},
		{"listUsers", s.listUsers, 500},
		{"getStaleDays", s.getStaleDays, 200},
		{"getEntry", s.getEntry, 404},
		{"genTOTP", s.genTOTP, 404},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/x", nil)
		c.call(w, r)
		if w.Code != c.want {
			t.Errorf("%s 关库应 %d，实际 %d body=%s", c.name, c.want, w.Code, w.Body.String())
		}
	}
	// createTemplate 携带合法 body：SaveTemplate 落库失败 → 400
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", nopCloser{strings.NewReader(`{"name":"n","category":"c","fields":[]}`)})
	s.createTemplate(w, r)
	if w.Code != 400 {
		t.Errorf("createTemplate 关库应 400，实际 %d", w.Code)
	}
	// 写路径（POST body 合法但查库失败）
	postCases := []struct {
		name string
		call func(w http.ResponseWriter, r *http.Request)
		body string
	}{
		{"createEntry", s.createEntry, `{"title":"t","fields":[]}`},
		{"createToken", s.createToken, `{"name":"x","scopes":["read"]}`},
		{"createUser", s.createUser, `{"username":"u","password":"12345678"}`},
		{"updateUser", s.updateUser, `{"disabled":true}`},
		{"importCSV", s.importCSV, `{"format":"chrome","csv":"name\nx\n"}`},
		{"saveAsTemplate", s.saveAsTemplate, `{"name":"n"}`},
		{"changePassword", s.changePassword, `{"old_password":"a1234567","new_password":"b1234567"}`},
		{"login", s.login, `{"username":"u","password":"p"}`},
		{"handleAIConfig", func(w http.ResponseWriter, r *http.Request) { s.handleAIConfig(w, r) }, `{"protocol":"openai","api_key":"sk-x","base_url":"http://x","model":"m"}`},
	}
	for _, c := range postCases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/x", nil)
		r.Body = http.NoBody
		if c.body != "" {
			r.Body = nopCloser{strings.NewReader(c.body)}
		}
		c.call(w, r)
		if w.Code < 400 && c.name != "importCSV" {
			t.Errorf("%s 关库应 >=400，实际 %d", c.name, w.Code)
		}
	}
}

type nopCloser struct{ r interface{ Read([]byte) (int, error) } }

func (n nopCloser) Read(b []byte) (int, error) { return n.r.Read(b) }
func (n nopCloser) Close() error               { return nil }

// 路由级错误分支：updateTemplate 坏 body、AI 配置坏密文、rotate 失败
func TestAPIErrorBranches2(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	// updateTemplate 坏 JSON → 400
	do(t, a, "POST", base+"/templates", `{"name":"tpl1","category":"c1","fields":[]}`)
	if code, _ := do(t, a, "PUT", base+"/templates/1", `{bad`); code != 400 {
		t.Fatalf("updateTemplate 坏 body 应 400，实际 %d", code)
	}

	// AI 对话：settings 里存坏密文 → 解密失败 500
	fx.DB.Exec(`INSERT INTO settings (key, value) VALUES ('ai_llm_api_key', 'enc:v1:$$$garbage$$$')
		ON CONFLICT(key) DO UPDATE SET value = 'enc:v1:$$$garbage$$$'`)
	if code, _ := do(t, a, "POST", base+"/ai-chat", `{"text":"hi"}`); code != 500 {
		t.Fatalf("坏密文 AI 对话应 500，实际 %d", code)
	}
	resp, err := a.Post(base+"/ai-chat/stream", "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("坏密文流式应 500，实际 %d", resp.StatusCode)
	}

	// rotate：条目坏数据 → RotateAllEntries 失败 → 500
	do(t, a, "POST", base+"/entries", `{"title":"R","category":"misc","fields":[{"key":"p","description":"密码","type":"text","is_secret":true,"value":"v"}]}`)
	fx.DB.Exec(`UPDATE entries SET fields = 'garbage-json'`)
	if code, _ := do(t, a, "POST", base+"/rotate-key", ""); code != 500 {
		t.Fatalf("坏数据轮换应 500，实际 %d", code)
	}
	// 恢复数据后：DataDir 指向一个已存在文件 → MkdirAll 失败 → 500（写文件分支）
	fx.DB.Exec(`UPDATE entries SET fields = '[{"key":"p","is_secret":true,"value":"enc:v1:AAA"}]'`)
	blockFile := srvBlockFile(t)
	fx.Store.DataDir = blockFile // 该路径是文件，MkdirAll(Dir(path)) 成功但目标处不可写
	fx.Store.KeyFilePath = blockFile + "/master.key"
	if code, out := do(t, a, "POST", base+"/rotate-key", ""); code != 500 {
		t.Fatalf("密钥写文件失败应 500，实际 %d %v", code, out)
	}
}

// srvBlockFile 返回一个已存在文件的路径（用于让写密钥文件失败）
func srvBlockFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "blocker")
	os.WriteFile(p, []byte("x"), 0o600)
	return p
}

// 零散错误分支：me 空上下文、AI 配置坏密文 GET、test/models 解密失败、putStaleDays 关库
func TestAPIErrorBranches3(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	// me：直调空 context → 401
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/auth/me", nil)
	s := &Server{Store: fx.Store}
	s.me(w, r)
	if w.Code != 401 {
		t.Fatalf("me 空 context 应 401，实际 %d", w.Code)
	}

	// AI 配置坏密文：GET 静默回 configured=false；test 500；models 静默降级继续
	fx.DB.Exec(`INSERT INTO settings (key, value) VALUES ('ai_llm_api_key', 'enc:v1:$$$bad$$$')
		ON CONFLICT(key) DO UPDATE SET value = 'enc:v1:$$$bad$$$'`)
	if code, out := do(t, a, "GET", base+"/ai-config", ""); code != 200 || out["configured"] != "false" {
		t.Fatalf("坏密文 GET 应静默: %d %v", code, out["configured"])
	}
	if code, out := do(t, a, "POST", base+"/ai-config/test", `{}`); code != 500 || out["ok"] != false {
		t.Fatalf("坏密文 test 应 500 ok=false: %d %v", code, out)
	}
	if code, out := do(t, a, "POST", base+"/ai-config/models",
		`{"base_url":"http://127.0.0.1:1","api_key":"k"}`); code != 200 || out["ok"] != false {
		t.Fatalf("models 失败端点应 ok=false: %d %v", code, out)
	}
}

// PRAGMA query_only：读正常、写失败——直调 handler（绕过会话中间件的续期写）打各写路径 500 分支
func TestWriteFailurePaths(t *testing.T) {
	fx := kdbtest.New(t)
	s := &Server{Store: fx.Store}

	// 准备：自建模板 + 普通用户 + 条目 + 令牌
	fx.Store.SaveTemplate(&model.Template{Name: "可删模板", Category: "deleteme", Fields: []model.Field{}})
	hash, _ := auth.HashPassword("temp-pw-123")
	fx.DB.Exec(`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES ('temp', ?, 0, '', '')`, hash)
	fx.Store.CreateEntry(&model.Entry{Title: "W", Category: "misc", Fields: []model.Field{}})
	auth.CreateAPIToken(fx.DB, "tk", []string{"read"}, nil)

	if _, err := fx.DB.Exec(`PRAGMA query_only = ON`); err != nil {
		t.Fatalf("无法设置 query_only: %v", err)
	}
	t.Cleanup(func() { fx.DB.Exec(`PRAGMA query_only = OFF`) })

	// invoke 经 chi 迷你路由直调（URLParam 正常注入，UserID 取空值）
	invoke := func(method, pattern, target, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		switch method {
		case "PUT":
			r.Put(pattern, h)
		case "POST":
			r.Post(pattern, h)
		case "DELETE":
			r.Delete(pattern, h)
		}
		w := httptest.NewRecorder()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, rd)
		r.ServeHTTP(w, req)
		return w
	}

	if w := invoke("PUT", "/t/{id}", "/t/1", `{"name":"改名失败"}`, s.updateTemplate); w.Code != 400 {
		t.Errorf("updateTemplate 只读应 400，实际 %d", w.Code)
	}
	// 自建模板的 id（内置模板从 1 开始且不可删）
	var tplID int64
	fx.DB.Exec(`PRAGMA query_only = OFF`)
	fx.DB.QueryRow(`SELECT id FROM category_templates WHERE category = 'deleteme'`).Scan(&tplID)
	fx.DB.Exec(`PRAGMA query_only = ON`)
	if w := invoke("DELETE", "/t/{id}", fmt.Sprintf("/t/%d", tplID), "", s.deleteTemplate); w.Code != 500 {
		t.Errorf("deleteTemplate 只读应 500，实际 %d", w.Code)
	}
	if w := invoke("DELETE", "/t/{id}", "/t/1", "", s.revokeToken); w.Code != 500 {
		t.Errorf("revokeToken 只读应 500，实际 %d", w.Code)
	}
	if w := invoke("PUT", "/s", "/s", `{"stale_days":30}`, s.putStaleDays); w.Code != 500 {
		t.Errorf("putStaleDays 只读应 500，实际 %d", w.Code)
	}
	if w := invoke("DELETE", "/u/{id}", "/u/2", "", s.deleteUser); w.Code != 500 {
		t.Errorf("deleteUser 只读应 500，实际 %d", w.Code)
	}
	if w := invoke("POST", "/rk", "/rk", "", s.rotateKey); w.Code != 500 {
		t.Errorf("rotateKey 只读应 500，实际 %d", w.Code)
	}
	if w := invoke("POST", "/u", "/u", `{"username":"ro","password":"ro-pw-12345"}`, s.createUser); w.Code != 500 {
		t.Errorf("createUser 只读应 500，实际 %d", w.Code)
	}
	if w := invoke("POST", "/e", "/e", `{"title":"RO","fields":[]}`, s.createEntry); w.Code != 400 {
		t.Errorf("createEntry 只读应 400，实际 %d", w.Code)
	}
	if w := invoke("PUT", "/e/{id}", "/e/1", `{"title":"RO2","fields":[]}`, s.updateEntry); w.Code != 400 {
		t.Errorf("updateEntry 只读应 400，实际 %d", w.Code)
	}
	if w := invoke("PUT", "/ai", "/ai", `{"model":"m-ro"}`, func(w http.ResponseWriter, r *http.Request) { s.handleAIConfig(w, r) }); w.Code != 500 {
		t.Errorf("ai-config 只读应 500，实际 %d", w.Code)
	}
	// import：逐条 CreateEntry 失败 → ok=0 fail 计数
	body := "{ \"format\": \"chrome\", \"csv\": \"name\\nX\\n\" }"
	if w := invoke("POST", "/i", "/i", strings.ReplaceAll(body, " ", ""), s.importCSV); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":0`) {
		t.Errorf("import 只读应 ok=0: %d %s", w.Code, w.Body.String())
	}
	// saveAsTemplate：写失败 → 400
	if w := invoke("POST", "/e/{id}/tpl", "/e/1/tpl", `{"name":"n"}`, s.saveAsTemplate); w.Code != 400 {
		t.Errorf("saveAsTemplate 只读应 400，实际 %d", w.Code)
	}
	// updateEntry 不存在 → 400（走 GetEntry err 而非写）
	if w := invoke("PUT", "/e/{id}", "/e/999", `{"title":"X","fields":[]}`, s.updateEntry); w.Code != 400 {
		t.Errorf("updateEntry 不存在应 400，实际 %d", w.Code)
	}
}
