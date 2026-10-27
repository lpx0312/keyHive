package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failSrv 所有请求返回 500
func failSrv(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestCliErrorBranches(t *testing.T) {
	// ---- 服务端 500 ----
	fs := failSrv(t)
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")

	t.Setenv("KEYHIVE_BASE_URL", fs.URL)
	if rc := Run([]string{"get", "1"}); rc != 1 {
		t.Fatal("get 500 应 1")
	}
	_ = os.Stdout // keep import

	cfg := &Config{BaseURL: fs.URL, TokenRead: "kh_read", TokenReveal: "kh_reveal"}
	if rc := cmdStatus(cfg); rc != 1 {
		t.Fatal("status 500 应 1")
	}
	if rc := cmdRotateKey(cfg, nil); rc != 1 {
		t.Fatal("rotate 500 应 1")
	}
	// add：喂 stdin 合法 JSON 后走到服务端 500
	oldStdin := os.Stdin
	sr, sw, _ := os.Pipe()
	sw.WriteString(`{"title":"x","fields":[]}`)
	sw.Close()
	os.Stdin = sr
	rcAdd := cmdAdd(cfg, nil)
	os.Stdin = oldStdin
	if rcAdd != 1 {
		t.Fatal("add 500 应 1")
	}
	// edit：GET 500 → 1
	if rc := cmdEdit(cfg, []string{"3", "title=x"}); rc != 1 {
		t.Fatal("edit GET 500 应 1")
	}

	// ---- edit 参数与流程 ----
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s", Path: "/"})
			w.Write([]byte(`{}`))
		case r.Method == "GET":
			w.Write([]byte(`{"id":3,"title":"T","ai_visible":true,"fields":[{"key":"user","type":"text","is_secret":false,"value":"u"},{"key":"pw","type":"text","is_secret":true,"value":"***"}]}`))
		case r.Method == "PUT":
			w.Write([]byte(`{"id":3}`))
		default:
			w.WriteHeader(500)
		}
	}))
	defer ok.Close()
	good := &Config{BaseURL: ok.URL}

	// 参数不足 / 坏 field 格式 / 无密码
	if rc := cmdEdit(good, []string{"3"}); rc != 2 {
		t.Fatal("edit 参数不足应 2")
	}
	if rc := cmdEdit(good, []string{"3", "noequal"}); rc != 2 {
		t.Fatal("edit 坏格式应 2")
	}
	t.Setenv("KEYHIVE_ADMIN_PASS", "")
	if rc := cmdEdit(good, []string{"3", "title=x"}); rc != 1 {
		t.Fatal("edit 无密码应 1")
	}
	if rc := cmdExport(good, nil); rc != 1 {
		t.Fatal("export 无密码应 1")
	}
	// import 无密码（非 dry-run、非 keyhive）
	csv := filepath.Join(t.TempDir(), "c.csv")
	os.WriteFile(csv, []byte("name,url,username,password\nX,u,p,w\n"), 0o600)
	if rc := cmdImport(good, []string{"--file", csv, "--format", "chrome"}); rc != 1 {
		t.Fatal("import 无密码应 1")
	}
	// import 文件不存在
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")
	if rc := cmdImport(good, []string{"--file", filepath.Join(t.TempDir(), "nope.csv"), "--format", "chrome", "--dry-run"}); rc != 1 {
		t.Fatal("import 文件不存在应 1")
	}
	// import keyhive 坏 JSON
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("not json"), 0o600)
	if rc := cmdImport(good, []string{"--file", bad, "--format", "keyhive"}); rc != 1 {
		t.Fatal("import keyhive 坏 JSON 应 1")
	}
	// import keyhive 无密码
	t.Setenv("KEYHIVE_ADMIN_PASS", "")
	kh := filepath.Join(t.TempDir(), "k.json")
	os.WriteFile(kh, []byte(`[{"title":"A","category":"misc","fields":[]}]`), 0o600)
	if rc := cmdImport(good, []string{"--file", kh, "--format", "keyhive"}); rc != 1 {
		t.Fatal("import keyhive 无密码应 1")
	}
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")
}

func TestCmdEditFullMatrix(t *testing.T) {
	var putBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s", Path: "/"})
			w.Write([]byte(`{}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/entries/5"):
			// 坏 JSON 响应分支用另一个 id
			w.Write([]byte(`{"id":5,"title":"T","category":"misc","description":"d","ai_visible":true,
				"fields":[{"key":"user","type":"text","is_secret":false,"value":"u"},
				          {"key":"pw","type":"text","is_secret":true,"value":"***"}]}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/entries/6"):
			w.Write([]byte(`{bad json`))
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/entries/5"):
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &putBody)
			w.Write([]byte(`{"id":5}`))
		default:
			w.WriteHeader(500)
		}
	}))
	defer ts.Close()

	cfg := &Config{BaseURL: ts.URL}
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")

	// 坏 JSON 响应
	if rc := cmdEdit(cfg, []string{"6", "title=x"}); rc != 1 {
		t.Fatal("edit 坏响应应 1")
	}
	// ai_visible 开关 + category/description + 敏感字段更新（值含 =）
	rc := cmdEdit(cfg, []string{"5", "ai_visible=false", "category=mysql", "description=新说明", "pw=a=b"})
	if rc != 0 {
		t.Fatalf("edit 组合更新应 0，实际 %d", rc)
	}
	if putBody["ai_visible"] != false || putBody["category"] != "mysql" || putBody["description"] != "新说明" {
		t.Fatalf("元数据更新错误: %v", putBody)
	}
	for _, f := range putBody["fields"].([]any) {
		m := f.(map[string]any)
		if m["key"] == "pw" && m["value"] != "a=b" {
			t.Fatalf("值含 = 应完整: %v", m["value"])
		}
	}
	// ai_visible=true 字符串
	if rc := cmdEdit(cfg, []string{"5", "ai_visible=1"}); rc != 0 {
		t.Fatal("ai_visible=1 应成功")
	}
}

func TestLoginErrorBranches(t *testing.T) {
	// 坏的现有配置
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte("{bad"), 0o600)
	t.Setenv("KEYHIVE_CONFIG", p)
	if rc := Run([]string{"login", "--url", "http://x:1", "--token-read", "t"}); rc != 1 {
		t.Fatal("login 坏配置应 1")
	}
	// 校验时服务挂 → 1
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none2.json"))
	t.Setenv("KEYHIVE_BASE_URL", "http://127.0.0.1:1")
	if rc := Run([]string{"login", "--url", "http://127.0.0.1:1", "--token-read", "t"}); rc != 1 {
		t.Fatal("login 服务挂应 1")
	}
}

func TestServiceDownThroughCommands(t *testing.T) {
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("KEYHIVE_BASE_URL", "http://127.0.0.1:1")
	// list（runROCapture err 路径）/ get（runRO err 路径）/ status（err hint）
	if rc := Run([]string{"list"}); rc != 1 {
		t.Fatal("list 服务挂应 1")
	}
	if rc := Run([]string{"get", "1"}); rc != 1 {
		t.Fatal("get 服务挂应 1")
	}
	if rc := Run([]string{"status"}); rc != 1 {
		t.Fatal("status 服务挂应 1")
	}
	if rc := Run([]string{"totp", "1"}); rc != 1 {
		t.Fatal("totp 服务挂应 1")
	}
}

func TestPromptSecretFallback(t *testing.T) {
	// masked=true 但 stdin 非终端：term.ReadPassword 失败 → bufio 回退
	old := os.Stdin
	r, w, _ := os.Pipe()
	w.WriteString("  token-value \n")
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old }()
	if got := promptSecret(true); got != "token-value" {
		t.Fatalf("回退读取错误: %q", got)
	}
}

func TestRunROServiceDownWithToken(t *testing.T) {
	// 有 token 但服务挂：走 runRO 的 err 分支（区别于 token 空分支）
	t.Setenv("KEYHIVE_CONFIG", func() string {
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, []byte(`{"token_read":"kh_x","token_reveal":"kh_y"}`), 0o600)
		return p
	}())
	t.Setenv("KEYHIVE_BASE_URL", "http://127.0.0.1:1")
	if rc := Run([]string{"get", "1"}); rc != 1 {
		t.Fatal("get 服务挂应 1")
	}
	if rc := Run([]string{"reveal", "1", "f"}); rc != 1 {
		t.Fatal("reveal 服务挂应 1")
	}
	if rc := Run([]string{"status"}); rc != 1 {
		t.Fatal("status 服务挂应 1")
	}
}

func TestImportFailurePaths(t *testing.T) {
	// 导入时服务端 500：chrome 与 keyhive 两条路径的 fail 计数
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s", Path: "/"})
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"boom"}`))
		}
	}))
	defer ts.Close()
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	cfg := &Config{BaseURL: ts.URL}

	csv := filepath.Join(t.TempDir(), "c.csv")
	os.WriteFile(csv, []byte("name,url,username,password\nX,u,p,w\n"), 0o600)
	if rc := cmdImport(cfg, []string{"--file", csv, "--format", "chrome"}); rc != 1 {
		t.Fatal("chrome 导入全失败应 1")
	}
	kh := filepath.Join(t.TempDir(), "k.json")
	os.WriteFile(kh, []byte(`[{"title":"A","category":"misc","fields":[]}]`), 0o600)
	if rc := cmdImport(cfg, []string{"--file", kh, "--format", "keyhive"}); rc != 1 {
		t.Fatal("keyhive 导入失败应 1")
	}
}

func TestCmdListStaleFilterBadData(t *testing.T) {
	// 响应非数组：filterStale 报错 → cmdList 返回 1
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"not array"}`))
	}))
	defer ts.Close()
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("KEYHIVE_BASE_URL", ts.URL)
	if rc := Run([]string{"list", "--stale", "90"}); rc != 1 {
		t.Fatal("非数组响应 --stale 应 1")
	}
}

func TestCmdExportMaskedBadResponse(t *testing.T) {
	// 导出响应非数组：--masked 解析失败 → 1
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s", Path: "/"})
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{"object":true}`))
		}
	}))
	defer ts.Close()
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	if rc := cmdExport(&Config{BaseURL: ts.URL}, []string{"--masked"}); rc != 1 {
		t.Fatal("非数组导出 --masked 应 1")
	}
}

func TestListErrorPaths(t *testing.T) {
	// 有效令牌 + 服务挂：cmdList → runROCapture 的 err + hint 分支
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"token_read":"kh_x","token_reveal":"kh_y"}`), 0o600)
	t.Setenv("KEYHIVE_CONFIG", p)
	t.Setenv("KEYHIVE_BASE_URL", "http://127.0.0.1:1")
	if rc := Run([]string{"list"}); rc != 1 {
		t.Fatal("list 服务挂应 1")
	}
	// 令牌无效：mock 401 → code!=200 → body 打印分支
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"bad token"}`))
	}))
	defer ts.Close()
	t.Setenv("KEYHIVE_BASE_URL", ts.URL)
	if rc := Run([]string{"list"}); rc != 1 {
		t.Fatal("list 401 应 1")
	}
}
