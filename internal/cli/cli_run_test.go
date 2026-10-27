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
	"time"
)

// mockFull 模拟一个 keyHive 服务端（AI 端点 + admin 端点），供 Run 全链路测试
type mockFull struct {
	srv      *httptest.Server
	addCount int
	exportOK bool
}

func newMockFull(t *testing.T) *mockFull {
	t.Helper()
	m := &mockFull{exportOK: true}
	mux := http.NewServeMux()

	login := func(w http.ResponseWriter, r *http.Request) {
		var creds map[string]string
		json.NewDecoder(r.Body).Decode(&creds)
		if creds["username"] == "admin" && creds["password"] == "pw" {
			http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s", Path: "/"})
			w.Write([]byte(`{"username":"admin"}`))
			return
		}
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"用户名或密码错误"}`))
	}
	mux.HandleFunc("/api/v1/auth/login", login)

	// AI 只读端点
	mux.HandleFunc("/api/v1/ai/entries", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kh_read" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"令牌无效"}`))
			return
		}
		old := time.Now().AddDate(0, 0, -120).UTC().Format(time.RFC3339)
		w.Write([]byte(`[{"id":1,"title":"ACR","category":"docker_registry","ai_visible":true,
			"updated_at":"` + old + `",
			"fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"***"},
			          {"key":"totp_secret","description":"两步验证 TOTP","type":"text","is_secret":true,"value":"***"}]},
			{"id":2,"title":"内网Git","category":"web_account","ai_visible":true,
			"updated_at":"` + time.Now().UTC().Format(time.RFC3339) + `",
			"fields":[]}]`))
	})
	mux.HandleFunc("/api/v1/ai/entries/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1,"title":"ACR","fields":[
			{"key":"password","description":"密码","type":"text","is_secret":true,"value":"***"},
			{"key":"totp_secret","description":"两步验证 TOTP","type":"text","is_secret":true,"value":"***"}]}`))
	})
	mux.HandleFunc("/api/v1/ai/entries/1/reveal", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kh_reveal" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"令牌无效"}`))
			return
		}
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		val := "super-secret"
		if req["field"] == "totp_secret" {
			val = "JBSWY3DPEHPK3PXP"
		}
		w.Write([]byte(`{"entry_id":1,"entry_title":"ACR","key":"` + req["field"] + `","value":"` + val + `"}`))
	})

	mux.HandleFunc("/api/v1/ai/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kh_read" {
			w.WriteHeader(401)
			return
		}
		if !strings.Contains(r.URL.RawQuery, "q=ACR") {
			w.Write([]byte(`[]`))
			return
		}
		w.Write([]byte(`[{"id":1,"title":"ACR","category":"docker_registry","fields":[]}]`))
	})

	// admin 端点
	mux.HandleFunc("/api/v1/entries", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			m.addCount++
			w.WriteHeader(201)
			w.Write([]byte(`{"id":9}`))
		}
	})
	mux.HandleFunc("/api/v1/export", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"title":"ACR","fields":[{"key":"password","is_secret":true,"value":"real"}]}]`))
	})
	mux.HandleFunc("/api/v1/rotate-key", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"entries":2,"key_file":"/data/master.key"}`))
	})
	mux.HandleFunc("/api/v1/entries/3", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":3,"title":"ACR","fields":[{"key":"username","type":"text","is_secret":false,"value":"u"}]}`))
	})

	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// cliEnv 配置 KEYHIVE_CONFIG/KEYHIVE_BASE_URL/KEYHIVE_ADMIN_PASS 指向 mock
func cliEnv(t *testing.T, m *mockFull, cfgContent string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if cfgContent != "" {
		os.WriteFile(p, []byte(cfgContent), 0o600)
	}
	t.Setenv("KEYHIVE_CONFIG", p)
	t.Setenv("KEYHIVE_BASE_URL", m.srv.URL)
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")
	return p
}

const goodCfg = `{"base_url":"http://placeholder","token_read":"kh_read","token_reveal":"kh_reveal"}`

// captureOS 捕获 stdout/stderr（Run 会打印）
func captureOS(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	done := make(chan struct{})
	go func() {
		o, _ := io.ReadAll(ro)
		e, _ := io.ReadAll(re)
		stdout, stderr = string(o), string(e)
		close(done)
	}()
	fn()
	wo.Close()
	we.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	<-done
	return
}

func TestRunDispatchAndUsage(t *testing.T) {
	m := newMockFull(t)
	cliEnv(t, m, goodCfg)

	// 无参数 / 未知命令 → 2
	if rc := Run(nil); rc != 2 {
		t.Fatalf("无参数应 2，实际 %d", rc)
	}
	if rc := Run([]string{"nope"}); rc != 2 {
		t.Fatalf("未知命令应 2，实际 %d", rc)
	}
	// 坏配置文件 → 1
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte("{bad"), 0o600)
	t.Setenv("KEYHIVE_CONFIG", p)
	if rc := Run([]string{"status"}); rc != 1 {
		t.Fatalf("坏配置应 1，实际 %d", rc)
	}
}

func TestRunStatusListSearchGet(t *testing.T) {
	m := newMockFull(t)
	cliEnv(t, m, goodCfg)

	if rc := Run([]string{"status"}); rc != 0 {
		t.Fatal("status 应 0")
	}
	out, _ := captureOS(t, func() { Run([]string{"list"}) })
	if !strings.Contains(out, "ACR") {
		t.Fatalf("list 输出应含条目: %s", out)
	}
	// list --stale 120：只剩超期条目
	out, _ = captureOS(t, func() { Run([]string{"list", "--stale", "120"}) })
	if !strings.Contains(out, "ACR") || strings.Contains(out, "内网Git") {
		t.Fatalf("--stale 过滤错误: %s", out)
	}
	// search
	out, _ = captureOS(t, func() { Run([]string{"search", "ACR"}) })
	if !strings.Contains(out, "ACR") {
		t.Fatalf("search 输出错误: %s", out)
	}
	// search 无关键词 → 2
	if rc := Run([]string{"search"}); rc != 2 {
		t.Fatal("search 无关键词应 2")
	}
	// get
	out, _ = captureOS(t, func() { Run([]string{"get", "1"}) })
	if !strings.Contains(out, "totp_secret") {
		t.Fatalf("get 输出错误: %s", out)
	}
	// get 参数数量错误 → 2
	if rc := Run([]string{"get"}); rc != 2 {
		t.Fatal("get 无 id 应 2")
	}
}

func TestRunRevealAndTOTP(t *testing.T) {
	m := newMockFull(t)
	cliEnv(t, m, goodCfg)

	out, _ := captureOS(t, func() { Run([]string{"reveal", "1", "password"}) })
	if !strings.Contains(out, "super-secret") {
		t.Fatalf("reveal 输出错误: %s", out)
	}
	// 参数数量错误 → 2
	if rc := Run([]string{"reveal", "1"}); rc != 2 {
		t.Fatal("reveal 参数不足应 2")
	}
	// totp：自动定位 totp_secret 字段 → 输出 6 位码
	out, _ = captureOS(t, func() { Run([]string{"totp", "1"}) })
	otp := strings.TrimSpace(out)
	if len(otp) != 6 {
		t.Fatalf("totp 应输出 6 位码，实际 %q", otp)
	}
	// totp --field password（非 base32 密钥）→ 1
	if rc := Run([]string{"totp", "1", "--field", "password"}); rc != 1 {
		t.Fatal("totp 非 base32 应 1")
	}
	// totp 无 id → 2
	if rc := Run([]string{"totp"}); rc != 2 {
		t.Fatal("totp 无 id 应 2")
	}

	// reveal token 未配置
	cliEnv(t, m, `{"base_url":"","token_read":"kh_read","token_reveal":""}`)
	if rc := Run([]string{"reveal", "1", "password"}); rc != 1 {
		t.Fatal("reveal 无令牌应 1")
	}
	if rc := Run([]string{"totp", "1"}); rc != 1 {
		t.Fatal("totp 无 reveal 令牌应 1")
	}
	// read 令牌未配置
	cliEnv(t, m, `{"token_read":""}`)
	if rc := Run([]string{"list"}); rc != 1 {
		t.Fatal("list 无 read 令牌应 1")
	}
	if rc := Run([]string{"get", "1"}); rc != 1 {
		t.Fatal("get 无 read 令牌应 1")
	}
	// read 令牌无效（服务端 401）
	cliEnv(t, m, `{"token_read":"kh_wrong"}`)
	if rc := Run([]string{"status"}); rc != 1 {
		t.Fatal("status 令牌无效应 1")
	}
	if rc := Run([]string{"list"}); rc != 1 {
		t.Fatal("list 令牌无效应 1")
	}
}

func TestRunAddExportImportRotate(t *testing.T) {
	m := newMockFull(t)
	cliEnv(t, m, goodCfg)

	// add：stdin JSON
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.WriteString(`{"title":"新条目","fields":[]}`)
	w.Close()
	os.Stdin = r
	rc := Run([]string{"add"})
	os.Stdin = oldStdin
	if rc != 0 || m.addCount != 1 {
		t.Fatalf("add 应 0 且服务端收到 1 条: rc=%d count=%d", rc, m.addCount)
	}

	// add 无密码 → 1
	t.Setenv("KEYHIVE_ADMIN_PASS", "")
	if rc := Run([]string{"add"}); rc != 1 {
		t.Fatal("add 无密码应 1")
	}
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")

	// add --file 坏 JSON → 2
	f := filepath.Join(t.TempDir(), "e.json")
	os.WriteFile(f, []byte(`{bad`), 0o600)
	if rc := Run([]string{"add", "--file", f}); rc != 2 {
		t.Fatal("add 坏 JSON 应 2")
	}
	// add --file 文件不存在 → 1
	if rc := Run([]string{"add", "--file", filepath.Join(t.TempDir(), "no.json")}); rc != 1 {
		t.Fatal("add 文件不存在应 1")
	}

	// export 明文 / --masked
	out, _ := captureOS(t, func() { Run([]string{"export"}) })
	if !strings.Contains(out, "real") {
		t.Fatalf("明文导出应含敏感值: %s", out)
	}
	out, _ = captureOS(t, func() { Run([]string{"export", "--masked"}) })
	if strings.Contains(out, `"real"`) || !strings.Contains(out, "***") {
		t.Fatalf("遮蔽导出不应含明文: %s", out)
	}
	// export 无密码 → 1
	t.Setenv("KEYHIVE_ADMIN_PASS", "")
	if rc := Run([]string{"export"}); rc != 1 {
		t.Fatal("export 无密码应 1")
	}
	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")

	// import chrome CSV：dry-run 0 条写入；真导入 2 条
	csvFile := filepath.Join(t.TempDir(), "c.csv")
	os.WriteFile(csvFile, []byte("name,url,username,password\nA,u1,p1,x\nB,u2,p2,y\n"), 0o600)
	before := m.addCount
	if rc := Run([]string{"import", "--file", csvFile, "--format", "chrome", "--dry-run"}); rc != 0 || m.addCount != before {
		t.Fatalf("dry-run 不应写入: rc=%d", rc)
	}
	if rc := Run([]string{"import", "--file", csvFile, "--format", "chrome"}); rc != 0 || m.addCount != before+2 {
		t.Fatalf("导入应成功 2 条: rc=%d add=%d", rc, m.addCount-before)
	}
	// import 无文件 → 2
	if rc := Run([]string{"import"}); rc != 2 {
		t.Fatal("import 无文件应 2")
	}
	// import 未知格式 → 1
	if rc := Run([]string{"import", "--file", csvFile, "--format", "nope", "--dry-run"}); rc != 1 {
		t.Fatal("import 未知格式应 1")
	}
	// import keyhive JSON
	khFile := filepath.Join(t.TempDir(), "k.json")
	os.WriteFile(khFile, []byte(`[{"title":"KH条目","category":"misc","fields":[{"key":"p","description":"密码","type":"text","is_secret":true,"value":"v"}]}]`), 0o600)
	before = m.addCount
	if rc := Run([]string{"import", "--file", khFile, "--format", "keyhive"}); rc != 0 || m.addCount != before+1 {
		t.Fatalf("keyhive 导入应成功: rc=%d", rc)
	}
	// import keyhive 遮蔽版（拒绝）
	maskedFile := filepath.Join(t.TempDir(), "m.json")
	os.WriteFile(maskedFile, []byte(`[{"title":"M","category":"misc","fields":[{"key":"p","type":"text","is_secret":true,"value":"***"}]}]`), 0o600)
	if rc := Run([]string{"import", "--file", maskedFile, "--format", "keyhive"}); rc != 1 {
		t.Fatal("keyhive 遮蔽版导入应 1")
	}

	// rotate-key
	out, _ = captureOS(t, func() { Run([]string{"rotate-key"}) })
	if !strings.Contains(out, "master.key") {
		t.Fatalf("rotate-key 输出错误: %s", out)
	}
	t.Setenv("KEYHIVE_ADMIN_PASS", "")
	if rc := Run([]string{"rotate-key"}); rc != 1 {
		t.Fatal("rotate-key 无密码应 1")
	}
}

func TestRunLoginNonInteractive(t *testing.T) {
	m := newMockFull(t)
	p := cliEnv(t, m, "")

	// 非交互且无参数 → 2
	if rc := Run([]string{"login"}); rc != 2 {
		t.Fatal("login 非交互无参数应 2")
	}
	// 非法 url → 2
	if rc := Run([]string{"login", "--url", "ftp://x"}); rc != 2 {
		t.Fatal("login 非法 url 应 2")
	}
	// 完整参数 + 校验通过 → 0，配置写入
	out, _ := captureOS(t, func() {
		Run([]string{"login", "--url", m.srv.URL, "--token-read", "kh_read", "--token-reveal", "kh_reveal"})
	})
	if !strings.Contains(out, "校验通过") {
		t.Fatalf("login 应校验通过: %s", out)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "kh_read") {
		t.Fatalf("配置应写入: %s", data)
	}
	// 合并更新：只改 reveal，read 保留
	Run([]string{"login", "--url", m.srv.URL, "--token-read", "kh_read", "--token-reveal", "kh_reveal2"})
	data, _ = os.ReadFile(p)
	var cfg Config
	json.Unmarshal(data, &cfg)
	if cfg.TokenReveal != "kh_reveal2" || cfg.TokenRead != "kh_read" {
		t.Fatalf("合并更新错误: %s", data)
	}
	// token 无效 → 校验失败 1
	if rc := Run([]string{"login", "--url", m.srv.URL, "--token-read", "kh_bad"}); rc != 1 {
		t.Fatal("login 校验失败应 1")
	}
	// --no-verify 跳过校验
	if rc := Run([]string{"login", "--url", m.srv.URL, "--token-read", "kh_x", "--no-verify"}); rc != 0 {
		t.Fatal("login --no-verify 应 0")
	}
}

func TestServiceDownPath(t *testing.T) {
	// 指向不存在的端口 → ErrServiceDown
	cfg := &Config{BaseURL: "http://127.0.0.1:1", TokenRead: "x"}
	_, _, err := ListEntries(cfg, "")
	if sd, ok := err.(*ErrServiceDown); !ok {
		t.Fatalf("应返回 ErrServiceDown: %v", err)
	} else if sd.hint() == "" || sd.Error() == "" {
		t.Fatal("hint/Error 不应为空")
	}
	// Run 层服务挂掉 → 1（ErrServiceDown 提示路径）
	t.Setenv("KEYHIVE_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	if rc := Run([]string{"list"}); rc != 0 { // token_read 未配置先拦
		if rc != 1 {
			t.Fatalf("应 1，实际 %d", rc)
		}
	}
}

func TestPromptHelpers(t *testing.T) {
	// promptLine：stdin 空输入 → 默认值
	old := os.Stdin
	r, w, _ := os.Pipe()
	w.Close() // EOF
	os.Stdin = r
	if got := promptLine("提示", "默认"); got != "默认" {
		t.Fatalf("EOF 应返回默认值: %q", got)
	}
	// promptSecret 非终端路径
	if got := promptSecret(false); got != "" {
		t.Fatalf("EOF 应返回空: %q", got)
	}
	os.Stdin = old
}

func TestSmallHelpers(t *testing.T) {
	if atoi("42") != 42 || atoi("x") != 0 || atoi("") != 0 {
		t.Fatal("atoi 错误")
	}
	if orDefault("", "d") != "d" || orDefault("v", "d") != "v" {
		t.Fatal("orDefault 错误")
	}
	if pick(true, "a", "b") != "a" || pick(false, "a", "b") != "b" {
		t.Fatal("pick 错误")
	}
	if sourceLabel("bitwarden") != "Bitwarden" || sourceLabel("chrome") != "Chrome" {
		t.Fatal("sourceLabel 错误")
	}
	if errOrBody(nil, []byte("body")) != "body" {
		t.Fatal("errOrBody body 分支错误")
	}
	if errOrBody(io.EOF, nil) != "EOF" {
		t.Fatal("errOrBody err 分支错误")
	}
	if !strings.Contains(tokenHint("token_read"), "token_read") {
		t.Fatal("tokenHint 应含字段名")
	}
	// countStale 坏 JSON → 0；filterStale 坏 JSON → err
	if n := countStale([]byte("bad"), 90); n != 0 {
		t.Fatal("countStale 坏 JSON 应 0")
	}
	if _, err := filterStale([]byte("bad"), 90); err == nil {
		t.Fatal("filterStale 坏 JSON 应报错")
	}
}
