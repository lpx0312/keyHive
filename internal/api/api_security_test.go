package api

// 本文件为 10 路代码审查后的盲区补测 + 缺陷复现测试：
//   - 普通测试为边界补测，应绿；
//   - TestBug_ 前缀的用例为缺陷复现（预期红）：断言代码审查报告中指出的"正确行为"，
//     在生产代码修复前会失败，禁止弱化断言或删除。
//
// 对应报告：
//   docs/review/01-review-http-api.md（P0 /ai-config 权限、P2 审计 detail 拼接、P2 请求体无上限、P3 整数溢出）

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ---- 盲区补测（应绿）----

// 登录端点畸形请求：错误方法 / 空体 / 数组体 / 类型不匹配 / null 体
func TestLoginMalformedRequests(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	c := client(t)

	// GET 打 POST-only 路由 → 405
	if code, _ := do(t, c, "GET", base+"/auth/login", ""); code != 405 {
		t.Fatalf("GET /auth/login 应 405，实际 %d", code)
	}
	// 空体 → 400
	if code, _ := do(t, c, "POST", base+"/auth/login", ""); code != 400 {
		t.Fatalf("空 body 应 400，实际 %d", code)
	}
	// JSON 数组无法解码进 struct → 400
	if code, _ := do(t, c, "POST", base+"/auth/login", `[1,2,3]`); code != 400 {
		t.Fatalf("数组 body 应 400，实际 %d", code)
	}
	// 字段类型不匹配 → 400
	if code, _ := do(t, c, "POST", base+"/auth/login", `{"username":123,"password":"x"}`); code != 400 {
		t.Fatalf("username 非字符串应 400，实际 %d", code)
	}
	// null 体可解码（零值 struct）→ 走凭据校验 → 401，绝不应 200/500
	if code := postStatus(t, c, base+"/auth/login", `null`); code != 401 {
		t.Fatalf("null body 应按凭据错误 401，实际 %d", code)
	}
	// 深层嵌套垃圾 → 400 而非 panic/500
	if code := postStatus(t, c, base+"/auth/login",
		`{"username":{"a":{"b":{"c":[1,2,{"d":null}]}}}}`); code != 400 {
		t.Fatalf("嵌套对象 body 应 400，实际 %d", code)
	}
}

// 条目 ID 解析边界：溢出字符串回绕后不得命中真实条目；前导零按数值匹配
func TestEntryIDParsingEdges(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	do(t, a, "POST", base+"/entries", `{"title":"唯一条目","category":"misc","fields":[]}`)

	// 前导零 = 1 → 正常命中
	if code, _ := do(t, a, "GET", base+"/entries/0000001", ""); code != 200 {
		t.Fatalf("前导零应等价于 1 → 200，实际 %d", code)
	}
	// 各类溢出/畸形：负数、int64 上界+1、20 位溢出串、100 位串、全角数字、16进制写法
	for _, id := range []string{
		"-1",
		"9223372036854775808",           // 2^63，回绕为负
		"99999999999999999999",          // 20 位，回绕
		strings.Repeat("9", 100),        // 超长溢出
		"１２３",                          // 全角数字
		"0x10",                          // 十六进制写法
		"1e3",                           // 科学计数
		strings.Repeat("0", 30) + "999", // 前导零 + 溢出尾部
	} {
		if code, _ := do(t, a, "GET", base+"/entries/"+id, ""); code != 404 {
			t.Fatalf("GET /entries/%s 应 404（不得命中真实条目），实际 %d", id, code)
		}
		if code, _ := do(t, a, "DELETE", base+"/entries/"+id, ""); code != 404 {
			t.Fatalf("DELETE /entries/%s 应 404，实际 %d", id, code)
		}
	}
}

// 审计分页参数边界：0/负数/非数字/超大/溢出串都不应 500
func TestAuditPaginationParamEdges(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	for i := 0; i < 2; i++ {
		do(t, a, "POST", base+"/entries", `{"title":"audit-edge","category":"misc","fields":[]}`)
	}

	queries := []string{
		"limit=0&offset=0",             // 0 → 默认值
		"limit=-5&offset=-3",            // 负数 → 默认值
		"limit=abc&offset=abc",          // 非数字 → 默认值
		"limit=99999",                   // 超大 → List 内封顶 200
		"limit=99999999999999999999",    // 溢出串回绕 → 封顶/默认
		"offset=99999999999999999999",   // 溢出串回绕 → 不报错
		"with_total=1&limit=99999",      // 组合
		"action=entry_create&limit=0",   // 带筛选
	}
	for _, q := range queries {
		code, out := do(t, a, "GET", base+"/audit?"+q, "")
		if code != 200 {
			t.Fatalf("/audit?%s 应 200，实际 %d %v", q, code, out)
		}
	}
	// 超大 limit 封顶：返回条数不超过 200
	resp, err := a.Get(base + "/audit?limit=99999&with_total=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page struct {
		Total int   `json:"total"`
		Logs  []any `json:"logs"`
	}
	json.NewDecoder(resp.Body).Decode(&page)
	if resp.StatusCode != 200 || len(page.Logs) > 200 {
		t.Fatalf("limit=99999 应封顶 ≤200 条，实际 %d（total=%d）", len(page.Logs), page.Total)
	}
	if page.Total < 1 {
		t.Fatalf("with_total 应给出 total≥1，实际 %d", page.Total)
	}
}

// 普通（非 admin）登录用户访问全部 admin 端点应 403：users/tokens/audit/export/rotate-key/import/ai-config/stale-days
func TestNonAdminDeniedOnAllAdminEndpoints(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	admin := client(t)
	do(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	if code, _ := do(t, admin, "POST", base+"/users", `{"username":"eve","password":"eve-pw-12345","is_admin":false}`); code != 201 {
		t.Fatal("创建 eve 失败")
	}
	eve := client(t)
	if code, _ := do(t, eve, "POST", base+"/auth/login", `{"username":"eve","password":"eve-pw-12345"}`); code != 200 {
		t.Fatal("eve 登录失败")
	}

	cases := []struct{ method, path, body string }{
		{"GET", "/users", ""},
		{"POST", "/users", `{"username":"x","password":"x-pw-12345"}`},
		{"PUT", "/users/1", `{"disabled":true}`},
		{"DELETE", "/users/1", ""},
		{"GET", "/tokens", ""},
		{"POST", "/tokens", `{"name":"x","scopes":["read"]}`},
		{"DELETE", "/tokens/1", ""},
		{"GET", "/audit", ""},
		{"GET", "/export", ""},
		{"POST", "/rotate-key", ""},
		{"POST", "/import", `{"format":"chrome","csv":"name\nx\n"}`},
		{"PUT", "/settings/stale-days", `{"stale_days":30}`},
		{"PUT", "/ai-config", `{"model":"m"}`},
	}
	for _, c := range cases {
		if code, out := do(t, eve, c.method, base+c.path, c.body); code != 403 {
			t.Fatalf("普通用户 %s %s 应 403，实际 %d %v", c.method, c.path, code, out)
		}
	}
}

// postStatus 只取状态码的 POST（body 无需解码为对象）
func postStatus(t *testing.T, c *http.Client, url, body string) int {
	t.Helper()
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// ---- 缺陷复现（预期红）----

// 【P0，预期红】api.go:84-86 + aichat_api.go:329-397 —— POST /ai-config/test 与
// POST /ai-config/models 挂在 RequireSession 普通用户组：请求体 Key 为空时服务端回退
// 解密已保存的管理员 LLM Key 并发往用户自填 base_url（凭据外带 + SSRF）。
// 正确行为：两个端点仅 admin 可用，普通登录用户应 403。
func TestBug_AIConfigTestRequiresAdmin(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	admin := client(t)
	do(t, admin, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	if code, _ := do(t, admin, "POST", base+"/users", `{"username":"mallory","password":"mallory-pw-123","is_admin":false}`); code != 201 {
		t.Fatalf("创建普通用户失败: %d", code)
	}
	mallory := client(t)
	if code, _ := do(t, mallory, "POST", base+"/auth/login", `{"username":"mallory","password":"mallory-pw-123"}`); code != 200 {
		t.Fatalf("mallory 登录失败: %d", code)
	}

	// base_url 指向必然拒绝的本地端口，避免测试触网；断言只看权限状态码
	code, out := do(t, mallory, "POST", base+"/ai-config/test", `{"base_url":"http://127.0.0.1:1"}`)
	if code != 403 {
		t.Errorf("[P0] 普通用户 POST /ai-config/test 应 403（仅 admin），实际 %d %v", code, out)
	}
	code, out = do(t, mallory, "POST", base+"/ai-config/models", `{"base_url":"http://127.0.0.1:1"}`)
	if code != 403 {
		t.Errorf("[P0] 普通用户 POST /ai-config/models 应 403（仅 admin），实际 %d %v", code, out)
	}
}

// 【P2，预期红】api.go:32-35 —— readBody 无 http.MaxBytesReader，全站（含未认证的
// /auth/login）不限制请求体大小，可未认证内存耗尽。正确行为：超限请求返回 413。
func TestBug_MaxBytesRejected(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	// 11MB 合法 JSON（超 10MB 上限）：当前实现会完整解码 → 401/400，而非 413
	big := strings.Repeat("a", 11<<20)
	c := client(t)
	resp, err := c.Post(base+"/auth/login", "application/json",
		bytes.NewReader([]byte(`{"username":"`+big+`","password":"x"}`)))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("[P2] >10MB 请求体应 413，实际 %d", resp.StatusCode)
	}
}

// 【P2，预期红】api.go:264 等 —— 审计 detail 由字符串拼接构造 JSON，条目标题含
// 英文双引号即产生非法 JSON（取证底线被破坏）。正确行为：detail 必须是合法 JSON。
func TestBug_AuditDetailValidJSON(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"

	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	// 标题含英文双引号与反斜杠（拼接场景下的两类破坏字符）
	title := `He said "hi" \ backslash`
	if code, out := do(t, a, "POST", base+"/entries",
		`{"title":`+jsonString(title)+`,"category":"misc","fields":[]}`); code != 201 {
		t.Fatalf("建条目应 201: %d %v", code, out)
	}

	// 直查 sqlite 审计表：entry_create 的 detail 必须可被 json.Unmarshal
	rows, err := fx.DB.Query(`SELECT detail FROM audit_logs WHERE action = 'entry_create'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		details = append(details, d)
	}
	if len(details) == 0 {
		t.Fatal("应存在 entry_create 审计记录")
	}
	for _, d := range details {
		var v any
		if err := json.Unmarshal([]byte(d), &v); err != nil {
			t.Errorf("[P2] 审计 detail 应为合法 JSON，实际 %q（解析错误: %v）", d, err)
		}
	}

	// 经 /api/v1/audit 读取同样校验（API 消费方视角）
	resp, err := a.Get(base + "/audit?action=entry_create&with_total=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page struct {
		Logs []struct {
			Detail string `json:"detail"`
		} `json:"logs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("审计响应解码失败: %v", err)
	}
	for _, l := range page.Logs {
		var v any
		if err := json.Unmarshal([]byte(l.Detail), &v); err != nil {
			t.Errorf("[P2] /audit 返回的 detail 应为合法 JSON，实际 %q（解析错误: %v）", l.Detail, err)
		}
	}
}
