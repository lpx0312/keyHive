package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPathEnvOverride(t *testing.T) {
	t.Setenv("KEYHIVE_CONFIG", "/tmp/x.json")
	if got := ConfigPath(); got != "/tmp/x.json" {
		t.Fatalf("KEYHIVE_CONFIG 未生效: %s", got)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		t.Fatalf("文件不存在应返回空配置: %v %v", cfg, err)
	}
}

func TestLoadConfigParse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`{"base_url":"http://x:1","token_read":"kh_a","token_reveal":"kh_b"}`), 0o600)
	t.Setenv("KEYHIVE_CONFIG", p)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "http://x:1" || cfg.TokenRead != "kh_a" || cfg.TokenReveal != "kh_b" {
		t.Fatalf("解析不符: %+v", cfg)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte("{not json"), 0o600)
	t.Setenv("KEYHIVE_CONFIG", p)
	if _, err := LoadConfig(); err == nil {
		t.Fatal("损坏 JSON 应报错")
	}
}

// TestSearchURLChinese 中文关键词必须正确 UTF-8 URL 编码（curl GBK 坑的回归测试）
func TestSearchURLChinese(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RawQuery
		w.Write([]byte("[]"))
	}))
	defer ts.Close()

	cfg := &Config{BaseURL: ts.URL, TokenRead: "kh_t"}
	_, code, err := SearchEntries(cfg, "华为")
	if err != nil || code != 200 {
		t.Fatalf("调用失败: %d %v", code, err)
	}
	if gotPath != "q=%E5%8D%8E%E4%B8%BA" {
		t.Fatalf("中文编码错误: %s", gotPath)
	}
}

func TestRevealBody(t *testing.T) {
	var body map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasSuffix(r.URL.Path, "/entries/7/reveal") {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	cfg := &Config{BaseURL: ts.URL, TokenReveal: "kh_r"}
	_, code, err := RevealField(cfg, "7", "password")
	if err != nil || code != 200 {
		t.Fatalf("调用失败: %d %v", code, err)
	}
	if body["field"] != "password" {
		t.Fatalf("body 错误: %v", body)
	}
}

func TestAddEntryLoginAndCreate(t *testing.T) {
	var gotCookie bool
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			var creds map[string]string
			json.NewDecoder(r.Body).Decode(&creds)
			if creds["username"] == "admin" && creds["password"] == "pw" {
				http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s1", Path: "/"})
				w.WriteHeader(200)
				w.Write([]byte(`{}`))
				return
			}
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"用户名或密码错误"}`))
		case "/api/v1/entries":
			c, err := r.Cookie("keyhive_session")
			gotCookie = err == nil && c.Value == "s1"
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(201)
			w.Write([]byte(`{"id":9}`))
		default:
			t.Errorf("意外请求: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	cfg := &Config{BaseURL: ts.URL}
	entry := []byte(`{"title":"ACR","fields":[{"key":"password","is_secret":true,"value":"x"}]}`)
	data, code, err := AddEntry(cfg, "admin", "pw", entry)
	if err != nil || code != 201 {
		t.Fatalf("录入失败: %d %v %s", code, err, data)
	}
	if !gotCookie {
		t.Fatal("创建请求未携带登录会话 cookie")
	}
	if gotBody["title"] != "ACR" {
		t.Fatalf("条目 body 错误: %v", gotBody)
	}

	// 错误密码 → 登录失败错误（adminCall 通道：err 必非空）
	_, _, err = AddEntry(cfg, "admin", "bad", entry)
	if err == nil {
		t.Fatal("错误密码应报登录失败")
	}
}
