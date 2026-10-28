package api

// 导出遮蔽测试：GET /export?masked=1（或 masked=true）返回遮蔽版导出——
// 响应 JSON 中不得出现任何真实敏感明文值，其余结构保持不变；
// 不带参数的行为保持明文导出（admin + 审计）不变。

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestExportMaskedParam(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	if code, _ := do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`); code != 200 {
		t.Fatal("admin 登录失败")
	}

	// 预置一条含敏感字段与非敏感字段的条目
	const secret = "super-secret-value-9527"
	entry := `{"title":"遮蔽导出","category":"web_account","description":"d","ai_visible":false,
	  "fields":[{"key":"username","description":"用户名","type":"text","is_secret":false,"value":"plain-user"},
	            {"key":"password","description":"密码","type":"text","is_secret":true,"value":"` + secret + `"}]}`
	if code, out := do(t, a, "POST", base+"/entries", entry); code != 201 {
		t.Fatalf("建条目失败: %d %v", code, out)
	}

	fetch := func(url string) string {
		t.Helper()
		resp, err := a.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s 应 200，实际 %d", url, resp.StatusCode)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// masked=1：整包响应文本中不得出现真实敏感明文
	masked := fetch(base + "/export?masked=1")
	if strings.Contains(masked, secret) {
		t.Fatalf("masked=1 导出不应包含任何敏感明文")
	}
	// 结构不变：仍为条目数组，标题/字段骨架保留，敏感值替换 ***
	var list []map[string]any
	if err := json.Unmarshal([]byte(masked), &list); err != nil {
		t.Fatalf("masked 导出应为合法 JSON 数组: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("masked 导出应保留全部条目，实际 %d 条", len(list))
	}
	if list[0]["title"] != "遮蔽导出" {
		t.Fatalf("非敏感结构应不变: %v", list[0]["title"])
	}
	found := false
	for _, f := range list[0]["fields"].([]any) {
		m, _ := f.(map[string]any)
		if m["key"] == "password" {
			found = true
			if m["is_secret"] != true || m["value"] != "***" {
				t.Fatalf("敏感字段值应替换为 ***: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("敏感字段应保留在导出结构中")
	}
	// 非敏感值不遮蔽
	if !strings.Contains(masked, "plain-user") {
		t.Fatal("非敏感值不应被遮蔽")
	}

	// masked=true 等价生效
	if m2 := fetch(base + "/export?masked=true"); strings.Contains(m2, secret) {
		t.Fatal("masked=true 同样不应包含敏感明文")
	}

	// 无参数：明文导出行为不变
	if plain := fetch(base + "/export"); !strings.Contains(plain, secret) {
		t.Fatal("无参数导出应保持明文")
	}
}
