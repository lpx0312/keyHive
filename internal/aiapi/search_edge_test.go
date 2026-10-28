package aiapi

// 审查盲区补测（应绿）：Search 路径隐身过滤 / fields JSON 匹配（非敏感可搜、密文不可搜、
// LIKE 通配符）/ 空结果恒返 []（历史 nil→null bug 回归锚点）/ GetEntry 直取隐身 404 /
// reveal 非敏感字段与不存在字段的行为（含审计写入情况）。

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lpx0312/keyHive/internal/model"
)

// TestSearchFiltersInvisible：search 结果中 ai_visible=false 条目必须隐身（此前只测了 list/get/reveal）
func TestSearchFiltersInvisible(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()
	mustCreateEntry(t, fx, model.Entry{Title: "生产SWR", Category: "huawei_swr", AIVisible: true,
		Fields: []model.Field{{Key: "host", Type: "text", Value: "swr.internal"}}})
	mustCreateEntry(t, fx, model.Entry{Title: "生产root", Category: "misc", AIVisible: false,
		Fields: []model.Field{{Key: "pw", Type: "text", IsSecret: true, Value: "rootpw"}}})

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/search?q=生产", nil)
	req.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []model.Entry
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 1 || list[0].Title != "生产SWR" {
		t.Fatalf("search 应过滤隐身条目，期望仅「生产SWR」，实际 %+v", list)
	}
	// 精确命中隐身条目标题也不可出现
	req2, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/search?q=root", nil)
	req2.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var list2 []model.Entry
	json.NewDecoder(resp2.Body).Decode(&list2)
	if len(list2) != 0 {
		t.Fatalf("隐身条目标题命中也不应返回: %+v", list2)
	}
}

// TestSearchMatchesFieldsJSON：非敏感字段值可搜（fields 是明文 JSON），
// 敏感字段值是密文（enc:v1:…）天然搜不到
func TestSearchMatchesFieldsJSON(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()
	mustCreateEntry(t, fx, model.Entry{Title: "数据库A", Category: "mysql", AIVisible: true,
		Fields: []model.Field{
			{Key: "host", Type: "text", Value: "10.0.0.99"},
			{Key: "password", Type: "text", IsSecret: true, Value: "plain-secret-xyz"},
		}})

	get := func(q string) []model.Entry {
		req, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/search?q="+q, nil)
		req.Header.Set("Authorization", "Bearer "+fx.RevealToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var list []model.Entry
		json.NewDecoder(resp.Body).Decode(&list)
		return list
	}
	if got := get("10.0.0.99"); len(got) != 1 {
		t.Fatalf("非敏感字段值应可搜到: %+v", got)
	}
	if got := get("plain-secret-xyz"); len(got) != 0 {
		t.Fatalf("敏感字段明文不应可搜（库里是密文）: %+v", got)
	}
}

// TestSearchLikeWildcard：q 中的 % / _ 作为 LIKE 通配符——当前实现等于全匹配（审查盲区锚点）
func TestSearchLikeWildcard(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()
	mustCreateEntry(t, fx, model.Entry{Title: "alpha", Category: "misc", AIVisible: true,
		Fields: []model.Field{{Key: "k", Type: "text", Value: "v"}}})
	mustCreateEntry(t, fx, model.Entry{Title: "beta", Category: "misc", AIVisible: true,
		Fields: []model.Field{{Key: "k", Type: "text", Value: "v"}}})
	mustCreateEntry(t, fx, model.Entry{Title: "gamma", Category: "misc", AIVisible: false,
		Fields: []model.Field{{Key: "k", Type: "text", Value: "v"}}})

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/search?q=%25", nil) // q=%
	req.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []model.Entry
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 2 {
		t.Fatalf("q=%% 作为通配符当前应匹配全部可见条目（2 条），实际 %d 条: %+v", len(list), list)
	}
}

// TestSearchEmptyResultAlwaysArray：无命中时必须是 [] 而非 null（历史 nil slice → JSON null 前端崩溃 bug 的回归锚点）
func TestSearchEmptyResultAlwaysArray(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()
	mustCreateEntry(t, fx, model.Entry{Title: "alpha", Category: "misc", AIVisible: true,
		Fields: []model.Field{{Key: "k", Type: "text", Value: "v"}}})

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/search?q=nothing-matches", nil)
	req.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	if _, err := io.Copy(&sb, resp.Body); err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(sb.String())
	if body != "[]" {
		t.Fatalf("空结果应恒返 []，实际 %q", body)
	}
}

// TestGetEntryInvisible404：ai_visible=false 条目 GetEntry 直取路径 404（与可见条目 200 对照）
func TestGetEntryInvisible404(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()
	vis := mustCreateEntry(t, fx, model.Entry{Title: "可见", Category: "misc", AIVisible: true,
		Fields: []model.Field{{Key: "pw", Type: "text", IsSecret: true, Value: "s"}}})
	invis := mustCreateEntry(t, fx, model.Entry{Title: "不可见", Category: "misc", AIVisible: false,
		Fields: []model.Field{{Key: "pw", Type: "text", IsSecret: true, Value: "s"}}})

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/entries/"+itoa(vis.ID), nil)
	req.Header.Set("Authorization", "Bearer "+fx.ReadToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var e model.Entry
	json.NewDecoder(resp.Body).Decode(&e)
	resp.Body.Close()
	if resp.StatusCode != 200 || e.FieldByKey("pw").Value != model.MaskedValue {
		t.Fatalf("可见条目应 200 且遮蔽: code=%d value=%v", resp.StatusCode, e.FieldByKey("pw"))
	}

	req2, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/entries/"+itoa(invis.ID), nil)
	req2.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("隐身条目直取应 404，实际 %d", resp2.StatusCode)
	}
}

// TestRevealNonSecretAndMissingField：reveal 不存在的字段名 404 且不写审计；
// 非敏感字段当前可 reveal（200 + 明文 + 审计）——锚定现状语义
func TestRevealNonSecretAndMissingField(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()
	e := mustCreateEntry(t, fx, model.Entry{Title: "混合字段", Category: "misc", AIVisible: true,
		Fields: []model.Field{
			{Key: "host", Type: "text", Value: "h1.example"},
			{Key: "password", Type: "text", IsSecret: true, Value: "sec"},
		}})
	base := srv.URL + "/api/v1/ai/entries/" + itoa(e.ID) + "/reveal"

	countAudit := func() int {
		var n int
		if err := fx.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, model.ActionFieldReveal).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 不存在的字段名 → 404，且不产生审计
	if n := countAudit(); n != 0 {
		t.Fatalf("前置审计数应 0，实际 %d", n)
	}
	req, _ := http.NewRequest("POST", base, strings.NewReader(`{"field":"no_such_field"}`))
	req.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("不存在的 field 应 404，实际 %d", resp.StatusCode)
	}
	if n := countAudit(); n != 0 {
		t.Fatalf("404 不应写审计，实际 %d 条", n)
	}

	// 非敏感字段 → 当前行为：200 返回明文并写审计（锚定现状）
	req2, _ := http.NewRequest("POST", base, strings.NewReader(`{"field":"host"}`))
	req2.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var out map[string]any
	json.NewDecoder(resp2.Body).Decode(&out)
	if resp2.StatusCode != 200 || out["value"] != "h1.example" {
		t.Fatalf("非敏感字段 reveal 现状应为 200+明文: code=%d out=%v", resp2.StatusCode, out)
	}
	if n := countAudit(); n != 1 {
		t.Fatalf("非敏感字段 reveal 现状应写 1 条审计，实际 %d", n)
	}
}
