package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/lpx0312/keyHive/internal/kdbtest"
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

// getJSON GET 数组/对象响应并解码（do() 只能解对象，/export /templates /entries 均返回数组）
func getJSON(t *testing.T, c *http.Client, url string) any {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s 应 200，实际 %d", url, resp.StatusCode)
	}
	var v any
	json.Unmarshal(b, &v)
	return v
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

// 导出 → 导入 round-trip：keyHive 明文导出 JSON 应能完整恢复；
// 遮蔽导出（敏感值 ***）必须在预览与导入两个入口都被拒绝。
func TestImportKeyhiveRoundTrip(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	admin := client(t)
	if code, _ := do(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`); code != 200 {
		t.Fatal("admin 登录失败")
	}

	// 预置一条含敏感字段的条目，再导出（/export 返回数组，绕过 do() 的对象解码拿原始 body）
	entry := `{"title":"ACR","category":"docker_registry","description":"d","ai_visible":true,
	  "fields":[{"key":"username","description":"用户名","type":"text","is_secret":false,"value":"lipanx"},
	            {"key":"password","description":"密码","type":"text","is_secret":true,"value":"real-pw"}]}`
	if code, out := do(t, admin, "POST", base+"/entries", entry); code != 201 {
		t.Fatalf("建条目失败: %d %v", code, out)
	}
	resp, err := admin.Get(base + "/export")
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("导出应 200，实际 %d", resp.StatusCode)
	}

	// 遮蔽版：敏感值替换 *** 后导入，预览与确认都应 400
	var list []map[string]any
	json.Unmarshal(exported, &list)
	for _, e := range list {
		for _, f := range e["fields"].([]any) {
			m := f.(map[string]any)
			if m["is_secret"] == true {
				m["value"] = "***"
			}
		}
	}
	masked, _ := json.Marshal(list)
	// 导出的内容是数组 JSON，须编码为 JSON 字符串放进 csv 字段
	csvMasked, _ := json.Marshal(string(masked))
	csvExported, _ := json.Marshal(string(exported))
	if code, _ := do(t, admin, "POST", base+"/import",
		`{"format":"keyhive","csv":`+string(csvMasked)+`,"dry_run":true}`); code != 400 {
		t.Fatalf("遮蔽版预览应 400，实际 %d", code)
	}
	if code, _ := do(t, admin, "POST", base+"/import",
		`{"format":"keyhive","csv":`+string(csvMasked)+`,"dry_run":false}`); code != 400 {
		t.Fatalf("遮蔽版导入应 400，实际 %d", code)
	}

	// dry_run 预览：不落库
	if code, out := do(t, admin, "POST", base+"/import",
		`{"format":"keyhive","csv":`+string(csvExported)+`,"dry_run":true}`); code != 200 || out["count"].(float64) != 1 {
		t.Fatalf("预览应 count=1: %d %v", code, out)
	}

	// 确认导入 → 再导出验证：2 条 ACR，恢复副本的敏感字段为明文原值
	if code, out := do(t, admin, "POST", base+"/import",
		`{"format":"keyhive","csv":`+string(csvExported)+`,"dry_run":false}`); code != 200 || out["ok"].(float64) != 1 {
		t.Fatalf("导入应成功 1 条: %d %v", code, out)
	}
	resp2, err := admin.Get(base + "/export")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	var finalList []map[string]any
	json.Unmarshal(after, &finalList)
	if len(finalList) != 2 {
		t.Fatalf("导入后应共 2 条条目，实际 %d", len(finalList))
	}
	restoredPw := ""
	for _, e := range finalList {
		if e["title"] == "ACR" && e["description"] == "d" {
			for _, f := range e["fields"].([]any) {
				m := f.(map[string]any)
				if m["key"] == "password" {
					restoredPw, _ = m["value"].(string)
				}
			}
		}
	}
	if restoredPw != "real-pw" {
		t.Fatalf("敏感字段明文应原样恢复，实际 %q", restoredPw)
	}

	// 垃圾 JSON 应 400
	if code, _ := do(t, admin, "POST", base+"/import", `{"format":"keyhive","csv":"not json"}`); code != 400 {
		t.Fatalf("非法 JSON 应 400，实际 %d", code)
	}
}

// 模板管理生命周期：自建模板增改删 + 内置模板允许编辑、禁止删除（新增/编辑均记审计）
func TestTemplateLifecycle(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	admin := client(t)
	if code, _ := do(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`); code != 200 {
		t.Fatal("admin 登录失败")
	}

	// 新建
	body := `{"name":"测试库tpl","group":"我的模板","category":"test_db","fields":[
	  {"key":"host","description":"地址","type":"text","is_secret":false},
	  {"key":"password","description":"密码","type":"text","is_secret":true}]}`
	if code, out := do(t, admin, "POST", base+"/templates", body); code != 201 {
		t.Fatalf("新建模板应 201: %d %v", code, out)
	}
	var created map[string]any
	for _, tpl := range getJSON(t, admin, base+"/templates").([]any) {
		if tpl.(map[string]any)["name"] == "测试库tpl" {
			created = tpl.(map[string]any)
		}
	}
	if created == nil {
		t.Fatal("新建模板应出现在列表")
	}
	id := fmt.Sprintf("%.0f", created["id"].(float64))

	// 编辑：改字段骨架
	upd := `{"name":"测试库tpl-v2","fields":[{"key":"host","description":"新地址说明","type":"text","is_secret":false}]}`
	if code, out := do(t, admin, "PUT", base+"/templates/"+id, upd); code != 200 {
		t.Fatalf("编辑模板应 200: %d %v", code, out)
	}
	for _, tpl := range getJSON(t, admin, base+"/templates").([]any) {
		m := tpl.(map[string]any)
		if m["id"] == created["id"] {
			if m["name"] != "测试库tpl-v2" || len(m["fields"].([]any)) != 1 {
				t.Fatalf("编辑未生效: %v", m)
			}
		}
	}

	// 内置模板：允许编辑、禁止删除
	var builtinID string
	for _, tpl := range getJSON(t, admin, base+"/templates").([]any) {
		if tpl.(map[string]any)["builtin"] == true {
			builtinID = fmt.Sprintf("%.0f", tpl.(map[string]any)["id"].(float64))
			break
		}
	}
	if builtinID == "" {
		t.Fatal("应存在内置模板")
	}
	if code, _ := do(t, admin, "PUT", base+"/templates/"+builtinID, `{"name":"内置改名测试"}`); code != 200 {
		t.Fatalf("内置模板应可编辑，实际 %d", code)
	}
	if code, out := do(t, admin, "DELETE", base+"/templates/"+builtinID, ""); code != 400 {
		t.Fatalf("内置模板删除应 400: %d %v", code, out)
	}

	// 自建删除
	if code, _ := do(t, admin, "DELETE", base+"/templates/"+id, ""); code != 200 {
		t.Fatalf("删除自建模板应 200，实际 %d", code)
	}
}

// 标签 round-trip + 搜索覆盖：q 应能命中 fields 里的 URL 等非敏感值与 tags
func TestTagsAndSearch(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	admin := client(t)
	if code, _ := do(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`); code != 200 {
		t.Fatal("admin 登录失败")
	}

	body := `{"title":"内网Git","category":"web_account","description":"d","ai_visible":true,
	  "tags":["公司内网"," 数据库 ","公司内网",""],
	  "fields":[{"key":"url","description":"地址","type":"url","is_secret":false,"value":"https://git.corp.local"},
	            {"key":"password","description":"密码","type":"text","is_secret":true,"value":"pw-123"}]}`
	if code, out := do(t, admin, "POST", base+"/entries", body); code != 201 {
		t.Fatalf("建条目应 201: %d %v", code, out)
	}

	// 单条读回：tags 归一化（去空、trim、去重）且原样保留
	code, one := do(t, admin, "GET", base+"/entries/1", "")
	if code != 200 {
		t.Fatalf("读单条应 200: %d", code)
	}
	got, _ := one["tags"].([]any)
	if len(got) != 2 || got[0] != "公司内网" || got[1] != "数据库" {
		t.Fatalf("tags 应归一化为 [公司内网 数据库]: %v", one["tags"])
	}

	// 搜索命中 URL 字段值
	_, list := doGet(t, admin, base+"/entries?q=git.corp.local")
	if len(list) != 1 {
		t.Fatalf("q=URL 应命中 1 条: %d", len(list))
	}
	// 搜索命中标签
	_, list = doGet(t, admin, base+"/entries?q=%E5%85%AC%E5%8F%B8%E5%86%85%E7%BD%91")
	if len(list) != 1 {
		t.Fatalf("q=标签(公司内网) 应命中 1 条: %d", len(list))
	}
	// 敏感值不应可搜
	_, list = doGet(t, admin, base+"/entries?q=pw-123")
	if len(list) != 0 {
		t.Fatalf("敏感字段明文不应可搜索: %d", len(list))
	}

	// 更新 tags
	if code, _ := do(t, admin, "PUT", base+"/entries/1",
		`{"title":"内网Git","category":"web_account","description":"d","ai_visible":true,
		  "tags":["公网"],"fields":[{"key":"url","description":"地址","type":"url","is_secret":false,"value":"https://git.corp.local"}]}`); code != 200 {
		t.Fatal("更新应 200")
	}
	_, list = doGet(t, admin, base+"/entries?q=%E5%85%AC%E7%BD%91")
	if len(list) != 1 {
		t.Fatal("更新后应命中新标签 公网")
	}
}

// doGet 原始 GET 并解 JSON 数组
func doGet(t *testing.T, c *http.Client, url string) (int, []any) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []any
	json.NewDecoder(resp.Body).Decode(&list)
	return resp.StatusCode, list
}
