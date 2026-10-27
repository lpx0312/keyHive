package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- 纯函数单元测试 ----

func TestParseInt64(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"123", 123},
		{"0", 0},
		{"", 0},
		{"abc", -1},
		{"12a", -1},
		{"-5", -1},
		{"1.2", -1},
		{"999999", 999999},
	}
	for _, c := range cases {
		if got := parseInt64(c.in); got != c.want {
			t.Errorf("parseInt64(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestClientIP(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.10:52311"
	if got := clientIP(r); got != "192.168.1.10" {
		t.Errorf("RemoteAddr 提取失败: %q", got)
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientIP(r); got != "1.2.3.4" {
		t.Errorf("XFF 首段提取失败: %q", got)
	}
	r.Header.Set("X-Forwarded-For", "  9.9.9.9 ")
	if got := clientIP(r); got != "9.9.9.9" {
		t.Errorf("XFF trim 失败: %q", got)
	}
	r2, _ := http.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.1"
	if got := clientIP(r2); got != "10.0.0.1" {
		t.Errorf("无端口 RemoteAddr: %q", got)
	}
}

func TestMaskedKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"ab", "****"},
		{"abcd", "****"},
		{"abcde", "****bcde"},
		{"sk-1234567890", "****7890"},
	}
	for _, c := range cases {
		if got := maskedKey(c.in); got != c.want {
			t.Errorf("maskedKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBoolJSONHelpers(t *testing.T) {
	if boolJSON(true) != "true" || boolJSON(false) != "false" {
		t.Error("boolJSON 错误")
	}
	if boolPtrJSON(nil) != "null" {
		t.Error("boolPtrJSON(nil) 应为 null")
	}
	v := true
	if boolPtrJSON(&v) != "true" {
		t.Error("boolPtrJSON(&true) 应为 true")
	}
}

func TestJoinScopes(t *testing.T) {
	if got := joinScopes([]string{"read", "reveal"}); got != `["read","reveal"]` {
		t.Errorf("joinScopes = %q", got)
	}
	if got := joinScopes(nil); got != "[]" {
		t.Errorf("joinScopes(nil) = %q", got)
	}
}

func TestIntQuery(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"", 50, 50},
		{"abc", 50, 50},
		{"0", 50, 50},
		{"5", 50, 5},
		{"999", 10, 999},
		{"1.5", 50, 50},
		{"-3", 50, 50},
	}
	for _, c := range cases {
		if got := intQuery(c.in, c.def); got != c.want {
			t.Errorf("intQuery(%q, %d) = %d, want %d", c.in, c.def, got, c.want)
		}
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault("", "x") != "x" || orDefault("  ", "x") != "x" || orDefault("v", "x") != "v" {
		t.Error("orDefault 逻辑错误")
	}
}

func TestFmtInt(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{-42, "-42"},
		{9223372036854775807, "9223372036854775807"},
	}
	for _, c := range cases {
		if got := fmtInt(c.in); got != c.want {
			t.Errorf("fmtInt(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestContainsFold(t *testing.T) {
	if !containsFold("MyTOTPSecret", "totp") || containsFold("abc", "z") {
		t.Error("containsFold 大小写折叠错误")
	}
	if !containsFold("两步验证码", "两步验证") {
		t.Error("containsFold 中文匹配失败")
	}
}

func TestDbNow(t *testing.T) {
	if len(dbNow()) != 20 || !strings.Contains(dbNow(), "T") {
		t.Errorf("dbNow 应为 RFC3339: %q", dbNow())
	}
}

// fieldOf 从已解码的 entry map 里取字段值
func fieldOf(t *testing.T, entry map[string]any, key string) map[string]any {
	t.Helper()
	fs, _ := entry["fields"].([]any)
	for _, f := range fs {
		m, _ := f.(map[string]any)
		if m["key"] == key {
			return m
		}
	}
	return nil
}

// ---- 认证边界 ----

func TestLoginEdgeCases(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	c := client(t)
	if code, out := do(t, c, "POST", base+"/auth/login", `{bad json`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d %v", code, out)
	}
	if code, out := do(t, c, "POST", base+"/auth/login", `{"username":"admin","password":"wrong-pw"}`); code != 401 {
		t.Fatalf("错误密码应 401，实际 %d %v", code, out)
	}
	if code, _ := do(t, c, "POST", base+"/auth/login", `{"username":" nosuch ","password":"wrong-pw"}`); code != 401 {
		t.Fatalf("不存在用户应 401，实际 %d", code)
	}
	var n int
	fx.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='login_failed'`).Scan(&n)
	if n < 2 {
		t.Fatalf("应有 login_failed 审计，实际 %d 条", n)
	}
	if code, _ := do(t, c, "POST", base+"/auth/login", `{"username":" admin ","password":"`+adminPW+`"}`); code != 200 {
		t.Fatalf("trim 后应登录成功，实际 %d", code)
	}
	c2 := client(t)
	if code, _ := do(t, c2, "GET", base+"/auth/me", ""); code != 401 {
		t.Fatalf("未登录 /auth/me 应 401，实际 %d", code)
	}
	// version 端点免认证
	if code, out := do(t, c2, "GET", base+"/version", ""); code != 200 || out["version"] == nil {
		t.Fatalf("version 应免认证 200: %d %v", code, out)
	}
}

func TestLogoutAndLogoutAll(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	if code, out := do(t, a, "POST", base+"/auth/logout", ""); code != 200 || out["ok"] != true {
		t.Fatalf("logout 应 200 ok: %d %v", code, out)
	}
	if code, _ := do(t, a, "GET", base+"/auth/me", ""); code != 401 {
		t.Fatalf("logout 后 /auth/me 应 401，实际 %d", code)
	}

	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	if code, _ := do(t, a, "POST", base+"/auth/logout-all", ""); code != 200 {
		t.Fatalf("logout-all 应 200，实际 %d", code)
	}
	if code, _ := do(t, a, "GET", base+"/entries", ""); code != 401 {
		t.Fatalf("logout-all 后应 401，实际 %d", code)
	}
}

func TestChangePasswordFlow(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	if code, _ := do(t, a, "POST", base+"/password", `{"old_password":"wrong","new_password":"new-pw-12345"}`); code != 400 {
		t.Fatalf("旧密码错应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/password", `{"old_password":"`+adminPW+`","new_password":"short"}`); code != 400 {
		t.Fatalf("短密码应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/password", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	if code, out := do(t, a, "POST", base+"/password", `{"old_password":"`+adminPW+`","new_password":"new-pw-12345"}`); code != 200 || out["ok"] != true {
		t.Fatalf("改密应 200: %d %v", code, out)
	}
	if code, _ := do(t, a, "GET", base+"/auth/me", ""); code != 401 {
		t.Fatalf("改密后旧会话应 401，实际 %d", code)
	}
	if code, _ := do(t, client(t), "POST", base+"/auth/login", `{"username":"admin","password":"new-pw-12345"}`); code != 200 {
		t.Fatalf("新密码应可登录，实际 %d", code)
	}
}

// ---- entries 边界 ----

func TestEntriesEdgeCases(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	if code, _ := do(t, a, "POST", base+"/entries", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/entries", `{"title":"","fields":[]}`); code != 400 {
		t.Fatalf("空标题应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/entries", `{"title":"t","fields":[{"key":"","description":"d","type":"text","is_secret":false,"value":"v"}]}`); code != 400 {
		t.Fatalf("空字段名应 400，实际 %d", code)
	}

	entry := `{"title":"E1","category":"web_account","fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"p@ss"}]}`
	do(t, a, "POST", base+"/entries", entry)
	for _, id := range []string{"999", "abc", "-1", "1.5"} {
		if code, _ := do(t, a, "GET", base+"/entries/"+id, ""); code != 404 {
			t.Fatalf("GET /entries/%s 应 404，实际 %d", id, code)
		}
	}
	if code, _ := do(t, a, "DELETE", base+"/entries/999", ""); code != 404 {
		t.Fatalf("DELETE 不存在应 404，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/entries/999/save-as-template", `{"name":"x"}`); code != 404 {
		t.Fatalf("saveAsTemplate 不存在应 404，实际 %d", code)
	}
	if code, _ := do(t, a, "PUT", base+"/entries/999", entry); code != 400 {
		t.Fatalf("PUT 不存在应 400，实际 %d", code)
	}

	// 默认遮蔽 vs reveal=true 明文 + 审计
	code, one := do(t, a, "GET", base+"/entries/1", "")
	if code != 200 {
		t.Fatalf("GET 应 200，实际 %d", code)
	}
	if f := fieldOf(t, one, "password"); f["value"] != "***" {
		t.Fatalf("默认响应敏感值应遮蔽: %v", f)
	}
	code, one = do(t, a, "GET", base+"/entries/1?reveal=true", "")
	if code != 200 {
		t.Fatalf("reveal 应 200，实际 %d", code)
	}
	if f := fieldOf(t, one, "password"); f["value"] != "p@ss" {
		t.Fatalf("reveal 应返回明文: %v", f)
	}
	var n int
	fx.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='entry_reveal'`).Scan(&n)
	if n != 1 {
		t.Fatalf("reveal 应记 1 条审计，实际 %d", n)
	}

	// 更新：*** 保留旧值
	upd := `{"title":"E1","category":"web_account","fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"***"}]}`
	if code, _ := do(t, a, "PUT", base+"/entries/1", upd); code != 200 {
		t.Fatalf("更新应 200，实际 %d", code)
	}
	_, one = do(t, a, "GET", base+"/entries/1?reveal=true", "")
	if f := fieldOf(t, one, "password"); f["value"] != "p@ss" {
		t.Fatalf("*** 更新应保留旧值，实际 %v", f["value"])
	}

	// 删除成功
	if code, _ := do(t, a, "DELETE", base+"/entries/1", ""); code != 200 {
		t.Fatalf("删除应 200，实际 %d", code)
	}

	// 分类列表：空时返回 []
	code = http.StatusOK
	_ = code
	resp, err := a.Get(base + "/categories")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var cats []string
	json.NewDecoder(resp.Body).Decode(&cats)
	if resp.StatusCode != 200 || cats == nil || len(cats) != 0 {
		t.Fatalf("空分类应返回 []，实际 %v (nil=%v)", cats, cats == nil)
	}
}

// saveAsTemplate：默认名 + 字段值清空（模板只存骨架）
func TestSaveAsTemplate(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	// 两个不同分类的条目（模板表 category 有 UNIQUE 约束，同条目重复另存会撞）
	do(t, a, "POST", base+"/entries",
		`{"title":"ACR","category":"docker_registry","fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"real"}]}`)
	do(t, a, "POST", base+"/entries",
		`{"title":"SWR","category":"cloud_registry","fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"real2"}]}`)

	// 指定名字
	if code, out := do(t, a, "POST", base+"/entries/1/save-as-template", `{"name":"我的ACR模板"}`); code != 201 {
		t.Fatalf("另存模板应 201: %d %v", code, out)
	}
	// 空 body → 默认名 "SWR 模板"；字段值被清空
	if code, out := do(t, a, "POST", base+"/entries/2/save-as-template", ``); code != 201 {
		t.Fatalf("默认名另存应 201: %d %v", code, out)
	}
	found := false
	for _, tpl := range getJSON(t, a, base+"/templates").([]any) {
		m := tpl.(map[string]any)
		if m["name"] == "SWR 模板" {
			found = true
			for _, f := range m["fields"].([]any) {
				fm := f.(map[string]any)
				if fm["key"] == "password" && fm["value"] != nil && fm["value"] != "" {
					t.Fatalf("模板不应携带字段值: %v", fm)
				}
			}
		}
	}
	if !found {
		t.Fatal("默认名模板应存在")
	}
}

// ---- TOTP ----

func TestGenTOTP(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	// 条目含 totp 字段（自动定位）+ 一个可指定但非 TOTP 的字段
	do(t, a, "POST", base+"/entries", `{"title":"带两步验证的站点","category":"web_account","fields":[
	  {"key":"totp_secret","description":"两步验证 TOTP","type":"text","is_secret":true,"value":"JBSWY3DPEHPK3PXP"},
	  {"key":"otp2","description":"备用验证器","type":"text","is_secret":true,"value":"JBSWY3DPEHPK3PXQ"}]}`)

	// 自动定位
	code, out := do(t, a, "POST", base+"/entries/1/totp", `{}`)
	if code != 200 {
		t.Fatalf("TOTP 生成应 200: %d %v", code, out)
	}
	codeStr, _ := out["code"].(string)
	if len(codeStr) != 6 {
		t.Fatalf("动态码应为 6 位数字: %v", out["code"])
	}
	for _, ch := range codeStr {
		if ch < '0' || ch > '9' {
			t.Fatalf("动态码应为纯数字: %q", codeStr)
		}
	}
	if rem, _ := out["remaining"].(float64); rem < 1 || rem > 30 {
		t.Fatalf("剩余秒数应在 1-30: %v", out["remaining"])
	}
	// 指定 field
	if code, _ := do(t, a, "POST", base+"/entries/1/totp", `{"field":"otp2"}`); code != 200 {
		t.Fatalf("指定 field 应 200，实际 %d", code)
	}
	// 不存在的条目
	if code, _ := do(t, a, "POST", base+"/entries/99/totp", `{}`); code != 404 {
		t.Fatalf("条目不存在应 404，实际 %d", code)
	}

	// 无 TOTP 字段的条目
	do(t, a, "POST", base+"/entries", `{"title":"无验证器","category":"misc","fields":[{"key":"x","description":"普通","type":"text","is_secret":false,"value":"1"}]}`)
	if code, _ := do(t, a, "POST", base+"/entries/2/totp", `{}`); code != 404 {
		t.Fatalf("无 TOTP 字段应 404，实际 %d", code)
	}

	// 无效密钥
	do(t, a, "POST", base+"/entries", `{"title":"坏密钥","category":"misc","fields":[{"key":"totp","description":"两步验证","type":"text","is_secret":true,"value":"!!!not-base32!!!"}]}`)
	if code, _ := do(t, a, "POST", base+"/entries/3/totp", `{}`); code != 400 {
		t.Fatalf("无效密钥应 400，实际 %d", code)
	}
}

// ---- CSV 导入（chrome / bitwarden / 边界）----

func TestImportCSV(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	// 空 csv / 坏 JSON
	if code, _ := do(t, a, "POST", base+"/import", `{"format":"chrome","csv":""}`); code != 400 {
		t.Fatalf("空 csv 应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/import", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	// 未知格式
	if code, _ := do(t, a, "POST", base+"/import", `{"format":"lastpass","csv":"x"}`); code != 400 {
		t.Fatalf("未知格式应 400，实际 %d", code)
	}

	chromeCSV := "name,url,username,password\nGitHub,https://github.com,octocat,gh-pw\nGitLab,https://gitlab.com,root,gl-pw\n"
	// dry_run：count=2，不落库
	if code, out := do(t, a, "POST", base+"/import", `{"format":"chrome","csv":`+jsonString(chromeCSV)+`,"dry_run":true}`); code != 200 || out["count"].(float64) != 2 {
		t.Fatalf("chrome 预览应 count=2: %d %v", code, out)
	}
	if _, list := doGet(t, a, base+"/entries"); len(list) != 0 {
		t.Fatalf("预览不应落库，实际 %d 条", len(list))
	}
	// 真导入
	if code, out := do(t, a, "POST", base+"/import", `{"format":"chrome","csv":`+jsonString(chromeCSV)+`}`); code != 200 || out["ok"].(float64) != 2 {
		t.Fatalf("chrome 导入应 ok=2: %d %v", code, out)
	}
	_, list := doGet(t, a, base+"/entries?q=octocat")
	if len(list) != 1 {
		t.Fatalf("导入后按用户名应命中 1 条，实际 %d", len(list))
	}

	// bitwarden 带 TOTP
	bwCSV := "folder,favorite,type,name,notes,fields,login_uri,login_username,login_password,login_totp\n,,1,BW站点,,," +
		"https://bw.example,u1,bw-pw,otpauth://totp/BW?secret=JBSWY3DPEHPK3PXP\n"
	if code, out := do(t, a, "POST", base+"/import", `{"format":"bitwarden","csv":`+jsonString(bwCSV)+`}`); code != 200 || out["ok"].(float64) != 1 {
		t.Fatalf("bitwarden 导入应 ok=1: %d %v", code, out)
	}
	if _, list := doGet(t, a, base+"/entries?q=BW站点"); len(list) != 1 {
		t.Fatalf("bitwarden 条目应可搜到，实际 %d", len(list))
	}

	// 仅表头无数据行 → 0 条
	if code, out := do(t, a, "POST", base+"/import", `{"format":"chrome","csv":`+jsonString("name,url,username,password\n")+`,"dry_run":true}`); code != 200 || out["count"].(float64) != 0 {
		t.Fatalf("仅表头应 count=0: %d %v", code, out)
	}
}

// jsonString 把任意字符串编码为 JSON 字符串字面量
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---- 轮换提醒阈值 ----

func TestStaleDays(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	// 默认 90
	if code, out := do(t, a, "GET", base+"/settings/stale-days", ""); code != 200 || out["stale_days"].(float64) != 90 {
		t.Fatalf("默认应为 90: %d %v", code, out)
	}
	// 边界：0 / 3651 / 坏 JSON / 非数字
	for _, body := range []string{`{"stale_days":0}`, `{"stale_days":3651}`, `{"stale_days":-5}`, `{bad`} {
		if code, _ := do(t, a, "PUT", base+"/settings/stale-days", body); code != 400 {
			t.Fatalf("PUT %s 应 400", body)
		}
	}
	// 正常写入 + 回读（含上下边界）
	for _, v := range []int{1, 3650, 180} {
		if code, out := do(t, a, "PUT", base+"/settings/stale-days", `{"stale_days":`+itoa(v)+`}`); code != 200 || out["stale_days"].(float64) != float64(v) {
			t.Fatalf("PUT %d 应 200: %d %v", v, code, out)
		}
		if code, out := do(t, a, "GET", base+"/settings/stale-days", ""); code != 200 || out["stale_days"].(float64) != float64(v) {
			t.Fatalf("GET 应回读 %d: %d %v", v, code, out)
		}
	}
	// 普通用户可读不可写
	b := client(t)
	do(t, a, "POST", base+"/users", `{"username":"u1","password":"u1-pw-12345","is_admin":false}`)
	do(t, b, "POST", base+"/auth/login", `{"username":"u1","password":"u1-pw-12345"}`)
	if code, _ := do(t, b, "GET", base+"/settings/stale-days", ""); code != 200 {
		t.Fatalf("普通用户 GET 应 200，实际 %d", code)
	}
	if code, _ := do(t, b, "PUT", base+"/settings/stale-days", `{"stale_days":30}`); code != 403 {
		t.Fatalf("普通用户 PUT 应 403，实际 %d", code)
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// ---- API 令牌 ----

func TestTokenEndpoints(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	if code, _ := do(t, a, "POST", base+"/tokens", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/tokens", `{"name":"","scopes":["read"]}`); code != 400 {
		t.Fatalf("空名应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/tokens", `{"name":"  ","scopes":["read"]}`); code != 400 {
		t.Fatalf("空白名应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "POST", base+"/tokens", `{"name":"t","scopes":["read"],"expires_at":"not-a-date"}`); code != 400 {
		t.Fatalf("非法过期时间应 400，实际 %d", code)
	}
	// scopes 白名单过滤 + 去重；token 明文只在创建响应
	code, out := do(t, a, "POST", base+"/tokens",
		`{"name":"cli-token","scopes":["read","reveal","bogus","reveal"],"expires_at":"2027-01-01T00:00:00Z"}`)
	if code != 201 {
		t.Fatalf("创建应 201，实际 %d %v", code, out)
	}
	if out["token"] == "" || out["token"] == nil {
		t.Fatalf("创建响应应含明文 token: %v", out)
	}
	scopes, _ := out["scopes"].([]any)
	if len(scopes) != 2 || scopes[0] != "read" || scopes[1] != "reveal" {
		t.Fatalf("scopes 应过滤为 [read reveal]: %v", scopes)
	}
	// 列表不回明文，且过期时间保留
	id := ""
	for _, tk := range getJSON(t, a, base+"/tokens").([]any) {
		m := tk.(map[string]any)
		if m["name"] == "cli-token" {
			id = fmt.Sprintf("%.0f", m["id"].(float64))
			if m["token"] != nil && m["token"] != "" {
				t.Fatalf("列表不应回明文: %v", m)
			}
			if m["expires_at"] == nil {
				t.Fatal("过期时间应保留")
			}
		}
	}
	if id == "" {
		t.Fatal("列表应含新令牌")
	}
	// 撤销（含不存在 id：SQL DELETE 静默成功）
	if code, out := do(t, a, "DELETE", base+"/tokens/"+id, ""); code != 200 || out["ok"] != true {
		t.Fatalf("撤销应 200: %d %v", code, out)
	}
	if code, _ := do(t, a, "DELETE", base+"/tokens/999", ""); code != 200 {
		t.Fatalf("撤销不存在 id 应 200（幂等），实际 %d", code)
	}
}

// ---- 用户管理边界 ----

func TestUserEndpointsEdgeCases(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	if code, _ := do(t, a, "POST", base+"/users", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "PUT", base+"/users/999", `{"disabled":true}`); code != 404 {
		t.Fatalf("更新不存在用户应 404，实际 %d", code)
	}
	if code, _ := do(t, a, "DELETE", base+"/users/999", ""); code != 404 {
		t.Fatalf("删除不存在用户应 404，实际 %d", code)
	}

	// 建普通用户并测试空更新/短密码
	do(t, a, "POST", base+"/users", `{"username":"carol","password":"carol-pw-123","is_admin":false}`)
	if code, _ := do(t, a, "PUT", base+"/users/2", `{}`); code != 400 {
		t.Fatalf("空更新应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "PUT", base+"/users/2", `{"password":"short"}`); code != 400 {
		t.Fatalf("短密码应 400，实际 %d", code)
	}
	if code, _ := do(t, a, "PUT", base+"/users/2", `{bad`); code != 400 {
		t.Fatalf("坏 JSON 应 400，实际 %d", code)
	}

	// 重置密码：旧会话被踢
	carol := client(t)
	do(t, carol, "POST", base+"/auth/login", `{"username":"carol","password":"carol-pw-123"}`)
	if code, _ := do(t, a, "PUT", base+"/users/2", `{"password":"carol-new-456"}`); code != 200 {
		t.Fatalf("重置密码应 200，实际 %d", code)
	}
	if code, _ := do(t, carol, "GET", base+"/entries", ""); code != 401 {
		t.Fatalf("重置密码后旧会话应 401，实际 %d", code)
	}

	// 用户列表
	list, ok := getJSON(t, a, base+"/users").([]any)
	if !ok || len(list) < 2 {
		t.Fatalf("用户列表应含 admin+carol: %v", list)
	}
}

// ---- 主密钥轮换：文件模式与 env 模式 ----

func TestRotateKeyFileMode(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	dir := t.TempDir()
	fx.Store.DataDir = dir
	fx.Store.KeyFilePath = ""
	fx.Store.KeyFromEnv = false

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	do(t, a, "POST", base+"/entries",
		`{"title":"轮换前","category":"misc","fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"before-rotate"}]}`)

	code, out := do(t, a, "POST", base+"/rotate-key", "")
	if code != 200 || out["ok"] != true {
		t.Fatalf("轮换应 200: %d %v", code, out)
	}
	if out["entries"].(float64) != 1 {
		t.Fatalf("应轮换 1 条，实际 %v", out["entries"])
	}
	if out["new_master_key"] != nil {
		t.Fatalf("文件模式不应返回新密钥明文: %v", out["new_master_key"])
	}
	keyFile, _ := out["key_file"].(string)
	if keyFile != filepath.Join(dir, "master.key") {
		t.Fatalf("key_file 应为 %s，实际 %q", filepath.Join(dir, "master.key"), keyFile)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Fatalf("密钥文件应落盘: %v", err)
	}
	// 轮换后旧条目仍可解密
	_, one := do(t, a, "GET", base+"/entries/1?reveal=true", "")
	if f := fieldOf(t, one, "password"); f["value"] != "before-rotate" {
		t.Fatalf("轮换后明文应不变: %v", f)
	}
	// 新条目用新密钥
	do(t, a, "POST", base+"/entries",
		`{"title":"轮换后","category":"misc","fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"after-rotate"}]}`)
	_, two := do(t, a, "GET", base+"/entries/2?reveal=true", "")
	if f := fieldOf(t, two, "password"); f["value"] != "after-rotate" {
		t.Fatalf("轮换后新条目应可解密: %v", f)
	}
	// 审计
	var n int
	fx.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='key_rotate'`).Scan(&n)
	if n != 1 {
		t.Fatalf("应记 key_rotate 审计，实际 %d", n)
	}
}

func TestRotateKeyEnvMode(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	fx.Store.KeyFromEnv = true

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	do(t, a, "POST", base+"/entries",
		`{"title":"env轮换","category":"misc","fields":[{"key":"password","description":"密码","type":"text","is_secret":true,"value":"v1"}]}`)

	code, out := do(t, a, "POST", base+"/rotate-key", "")
	if code != 200 {
		t.Fatalf("轮换应 200，实际 %d %v", code, out)
	}
	if out["new_master_key"] == nil || out["new_master_key"] == "" {
		t.Fatalf("env 模式应返回新密钥: %v", out)
	}
	if kf, _ := out["key_file"].(string); kf != "" {
		t.Fatalf("env 模式 key_file 应为空，实际 %q", kf)
	}
	// 密文仍可解密
	_, one := do(t, a, "GET", base+"/entries/1?reveal=true", "")
	if f := fieldOf(t, one, "password"); f["value"] != "v1" {
		t.Fatalf("env 轮换后明文应不变: %v", f)
	}
}

// 非流式 update 草稿（DraftEntryID>0 的审计分支）
func TestAIChatUpdateDraftAudit(t *testing.T) {
	tool := map[string]any{
		"id": "call-u", "type": "function",
		"function": map[string]any{
			"name": "update_entry_draft",
			"arguments": map[string]any{
				"entry_id": 7, "title": "改名条目", "category": "misc",
				"fields": []any{map[string]any{"key": "k", "description": "d", "value": "v"}},
			},
		},
	}
	ml := newMockLLM(t, "", []map[string]any{tool}, nil)
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	do(t, a, "PUT", base+"/ai-config",
		`{"protocol":"openai","base_url":`+jsonString(ml.srv.URL)+`,"api_key":"sk-x","model":"m"}`)
	code, out := do(t, a, "POST", base+"/ai-chat", `{"text":"改一下"}`)
	if code != 200 || out["draft_kind"] != "update" {
		t.Fatalf("update 草稿失败: %d %v", code, out)
	}
	var n int
	fx.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='ai_chat_draft' AND detail LIKE '%entry_id%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("update 草稿审计应含 entry_id，实际 %d 条", n)
	}
}
