package aiapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"keyhive/internal/auth"
	"keyhive/internal/kdbtest"
	"keyhive/internal/model"
)

// newTestServer 搭建与 main 相同结构的路由（内存库 + 随机主密钥）
func newTestServer(t *testing.T) (*httptest.Server, *kdbtest.Fixture) {
	t.Helper()
	fx := kdbtest.New(t)
	st := fx.Store
	ai := &Server{Store: st}

	r := chi.NewRouter()
	r.Route("/api/v1/ai", func(ar chi.Router) {
		ar.Group(func(g chi.Router) {
			g.Use(auth.RequireToken(fx.DB, "read"))
			g.Get("/entries", ai.ListEntries)
			g.Get("/entries/{id}", ai.GetEntry)
		})
		ar.Group(func(g chi.Router) {
			g.Use(auth.RequireToken(fx.DB, "search"))
			g.Get("/search", ai.Search)
		})
		ar.Group(func(g chi.Router) {
			g.Use(auth.RequireToken(fx.DB, "reveal"))
			g.Post("/entries/{id}/reveal", ai.Reveal)
		})
	})
	return httptest.NewServer(r), fx
}

func mustCreateEntry(t *testing.T, fx *kdbtest.Fixture, e model.Entry) *model.Entry {
	t.Helper()
	if err := fx.Store.CreateEntry(&e); err != nil {
		t.Fatal(err)
	}
	return &e
}

func TestListMasksSecretsAndHidesInvisible(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()

	mustCreateEntry(t, fx, model.Entry{
		Title: "华为SWR-生产", Category: "huawei_swr", AIVisible: true,
		Description: "生产 CI 拉镜像用",
		Fields: []model.Field{
			{Key: "org", Description: "组织名称", Type: "text", Value: "prod-team"},
			{Key: "password", Description: "docker login 密码", Type: "text", IsSecret: true, Value: "s3cret-密码"},
		},
	})
	secretEntry := mustCreateEntry(t, fx, model.Entry{
		Title: "root密码", Category: "misc", AIVisible: false,
		Fields: []model.Field{{Key: "pw", Type: "text", IsSecret: true, Value: "rootpw"}},
	})

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/entries", nil)
	req.Header.Set("Authorization", "Bearer "+fx.ReadToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var list []model.Entry
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("ai_visible=false 条目应隐身，期望 1 条实际 %d", len(list))
	}
	for _, f := range list[0].Fields {
		if f.Key == "password" {
			if f.Value != model.MaskedValue {
				t.Fatalf("敏感值未遮蔽: %q", f.Value)
			}
		}
		if f.Key == "org" && f.Value != "prod-team" {
			t.Fatalf("非敏感值被错误遮蔽: %q", f.Value)
		}
	}
	if f := list[0].FieldByKey("password"); f != nil && f.Description != "docker login 密码" {
		t.Fatal("注释应原样返回")
	}

	// 隐身条目直接按 ID 访问也应 404
	req2, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/entries/"+itoa(secretEntry.ID), nil)
	req2.Header.Set("Authorization", "Bearer "+fx.ReadToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("隐身条目详情应 404，实际 %d", resp2.StatusCode)
	}
}

func TestRevealRequiresScopeAndWritesAudit(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()

	e := mustCreateEntry(t, fx, model.Entry{
		Title: "SWR", Category: "huawei_swr", AIVisible: true,
		Fields: []model.Field{
			{Key: "password", Description: "docker login 密码", Type: "text", IsSecret: true, Value: "明文P@ss"},
		},
	})
	url := srv.URL + "/api/v1/ai/entries/" + itoa(e.ID) + "/reveal"

	// 无 reveal scope → 403
	req, _ := http.NewRequest("POST", url, strings.NewReader(`{"field":"password"}`))
	req.Header.Set("Authorization", "Bearer "+fx.ReadToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only token 调 reveal 应 403，实际 %d", resp.StatusCode)
	}

	// 无 token → 401
	req2, _ := http.NewRequest("POST", url, strings.NewReader(`{"field":"password"}`))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无令牌应 401，实际 %d", resp2.StatusCode)
	}

	// reveal token → 200 明文 + 审计
	req3, _ := http.NewRequest("POST", url, strings.NewReader(`{"field":"password"}`))
	req3.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("reveal 应 200，实际 %d", resp3.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp3.Body).Decode(&out)
	if out["value"] != "明文P@ss" {
		t.Fatalf("reveal 值不符: %v", out["value"])
	}
	if out["description"] != "docker login 密码" {
		t.Fatalf("reveal 应带注释: %v", out["description"])
	}

	var action, actorType string
	err = fx.DB.QueryRow(`SELECT action, actor_type FROM audit_logs ORDER BY id DESC LIMIT 1`).Scan(&action, &actorType)
	if err != nil {
		t.Fatal(err)
	}
	if action != model.ActionFieldReveal || actorType != "token" {
		t.Fatalf("reveal 未写审计: action=%s actor=%s", action, actorType)
	}
}

func TestSearchScopeSeparate(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()
	mustCreateEntry(t, fx, model.Entry{Title: "生产数据库", Category: "mysql", AIVisible: true,
		Fields: []model.Field{{Key: "host", Type: "text", Value: "10.0.0.1"}}})

	// read token 无 search scope → 403
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/search?q=数据库", nil)
	req.Header.Set("Authorization", "Bearer "+fx.ReadToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("search 应需要 search scope，实际 %d", resp.StatusCode)
	}

	// reveal token（同时含 search）→ 200
	req2, _ := http.NewRequest("GET", srv.URL+"/api/v1/ai/search?q=数据库", nil)
	req2.Header.Set("Authorization", "Bearer "+fx.RevealToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("search 应 200，实际 %d", resp2.StatusCode)
	}
	var list []model.Entry
	json.NewDecoder(resp2.Body).Decode(&list)
	if len(list) != 1 || list[0].Title != "生产数据库" {
		t.Fatalf("搜索结果不符: %+v", list)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
