package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStaleFilterAndCount(t *testing.T) {
	old := time.Now().AddDate(0, 0, -120).UTC().Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	data, _ := json.Marshal([]staleEntry{{UpdatedAt: old}, {UpdatedAt: fresh}, {UpdatedAt: old}})

	if n := countStale(data, 90); n != 2 {
		t.Fatalf("countStale 期望 2，实际 %d", n)
	}
	filtered, err := filterStale(data, 90)
	if err != nil {
		t.Fatal(err)
	}
	var list []staleEntry
	json.Unmarshal(filtered, &list)
	if len(list) != 2 {
		t.Fatalf("filterStale 期望 2 条，实际 %d", len(list))
	}
}

func TestParseListAndSearchArgs(t *testing.T) {
	if c, d := parseListArgs([]string{"--category", "mysql", "--stale", "30"}); c != "mysql" || d != 30 {
		t.Fatalf("parseListArgs: %s %d", c, d)
	}
	if q, d := parseSearchArgs([]string{"华为", "--stale", "90"}); q != "华为" || d != 90 {
		t.Fatalf("parseSearchArgs: %s %d", q, d)
	}
	if q, _ := parseSearchArgs([]string{"--stale", "90", "x"}); q != "x" {
		t.Fatalf("关键词在后的解析: %q", q)
	}
}

func TestLoadConfigBaseURLOverride(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/c.json"
	os.WriteFile(p, []byte(`{"base_url":"http://from-file:1"}`), 0o600)
	t.Setenv("KEYHIVE_CONFIG", p)

	cfg, err := LoadConfig()
	if err != nil || cfg.BaseURL != "http://from-file:1" {
		t.Fatalf("无覆盖时应读文件: %+v %v", cfg, err)
	}

	t.Setenv("KEYHIVE_BASE_URL", "http://other-host:8020")
	cfg, err = LoadConfig()
	if err != nil || cfg.BaseURL != "http://other-host:8020" {
		t.Fatalf("KEYHIVE_BASE_URL 应覆盖: %+v %v", cfg, err)
	}
}

func TestCmdEditFlow(t *testing.T) {
	// 假服务：GET 返回遮蔽条目，PUT 校验修改后的 body
	var putBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "keyhive_session", Value: "s", Path: "/"})
			w.Write([]byte(`{}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/entries/3"):
			w.Write([]byte(`{"id":3,"title":"ACR","category":"docker_registry","ai_visible":true,
				"fields":[
					{"key":"username","description":"用户名","type":"text","is_secret":false,"value":"lipanx"},
					{"key":"password","description":"密码","type":"text","is_secret":true,"value":"***"}]}`))
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/entries/3"):
			json.NewDecoder(r.Body).Decode(&putBody)
			w.Write([]byte(`{"id":3,"title":"ACR"}`))
		default:
			t.Errorf("意外请求: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	t.Setenv("KEYHIVE_ADMIN_PASS", "pw")
	cfg := &Config{BaseURL: ts.URL}
	old := os.Stdout
	r, w2, _ := os.Pipe()
	os.Stdout = w2
	rc := cmdEdit(cfg, []string{"3", "password=NewP@ss", "username=li", "title=新标题"})
	w2.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old
	_ = out

	if rc != 0 {
		t.Fatalf("edit 返回码 %d", rc)
	}
	if putBody["title"] != "新标题" {
		t.Fatalf("title 未更新: %v", putBody["title"])
	}
	fields := putBody["fields"].([]any)
	for _, f := range fields {
		m := f.(map[string]any)
		if m["key"] == "username" && m["value"] != "li" {
			t.Fatalf("username 未更新: %v", m["value"])
		}
		if m["key"] == "password" && m["value"] != "NewP@ss" {
			t.Fatalf("password 未更新: %v", m["value"])
		}
	}

	// 不存在的字段 → 报错
	if rc := cmdEdit(cfg, []string{"3", "nope=1"}); rc == 0 {
		t.Fatal("未知字段应报错")
	}
	// 敏感字段显式 *** → 报错
	if rc := cmdEdit(cfg, []string{"3", "password=***"}); rc == 0 {
		t.Fatal("敏感字段设 *** 应报错")
	}
}
