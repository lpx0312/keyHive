package aiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lpx0312/keyHive/internal/kdbtest"
	"github.com/lpx0312/keyHive/internal/model"
)

func TestParseInt64(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"123", 123}, {"", 0}, {"abc", -1}, {"1x", -1}, {"-9", -1},
	}
	for _, c := range cases {
		if got := parseInt64(c.in); got != c.want {
			t.Errorf("parseInt64(%q)=%d want %d", c.in, got, c.want)
		}
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	fx := kdbtest.New(t)
	s := &Server{Store: fx.Store}
	w := &respRecorder{header: http.Header{}}
	r, _ := http.NewRequest("GET", "/search?q=+", nil)
	s.Search(w, r)
	if w.code != 400 {
		t.Fatalf("空白 q 应 400，实际 %d", w.code)
	}
	r2, _ := http.NewRequest("GET", "/search", nil)
	s.Search(w, r2)
	if w.code != 400 {
		t.Fatalf("缺 q 应 400，实际 %d", w.code)
	}
}

func TestRevealEdgeCases(t *testing.T) {
	srv, fx := newTestServer(t)
	defer srv.Close()

	e := mustCreateEntry(t, fx, model.Entry{
		Title: "E", Category: "misc", AIVisible: true,
		Fields: []model.Field{{Key: "pw", Type: "text", IsSecret: true, Value: "v"}},
	})
	invis := mustCreateEntry(t, fx, model.Entry{
		Title: "隐藏", Category: "misc", AIVisible: false,
		Fields: []model.Field{{Key: "pw", Type: "text", IsSecret: true, Value: "v"}},
	})
	base := srv.URL + "/api/v1/ai"

	post := func(path, body string) int {
		req, _ := http.NewRequest("POST", base+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+fx.RevealToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	// 坏 body / 空 field
	if code := post("/entries/"+itoa(e.ID)+"/reveal", `{bad`); code != 400 {
		t.Fatalf("坏 body 应 400，实际 %d", code)
	}
	if code := post("/entries/"+itoa(e.ID)+"/reveal", `{"field":"  "}`); code != 400 {
		t.Fatalf("空 field 应 400，实际 %d", code)
	}
	// 字段不存在
	if code := post("/entries/"+itoa(e.ID)+"/reveal", `{"field":"nope"}`); code != 404 {
		t.Fatalf("字段不存在应 404，实际 %d", code)
	}
	// 隐身条目
	if code := post("/entries/"+itoa(invis.ID)+"/reveal", `{"field":"pw"}`); code != 404 {
		t.Fatalf("隐身条目 reveal 应 404，实际 %d", code)
	}
	// 条目不存在 / 非法 id
	if code := post("/entries/999/reveal", `{"field":"pw"}`); code != 404 {
		t.Fatalf("不存在条目应 404，实际 %d", code)
	}
	if code := post("/entries/abc/reveal", `{"field":"pw"}`); code != 404 {
		t.Fatalf("非法 id 应 404，实际 %d", code)
	}

	// GetEntry 不存在 / 非法 id
	get := func(path string) int {
		req, _ := http.NewRequest("GET", base+path, nil)
		req.Header.Set("Authorization", "Bearer "+fx.ReadToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/entries/999"); code != 404 {
		t.Fatalf("不存在条目应 404，实际 %d", code)
	}
	if code := get("/entries/xyz"); code != 404 {
		t.Fatalf("非法 id 应 404，实际 %d", code)
	}

	// tokenInfo 空 context
	s := &Server{Store: fx.Store}
	r, _ := http.NewRequest("GET", "/", nil)
	if id, name := s.tokenInfo(r.WithContext(context.Background())); id != 0 || name != "" {
		t.Fatal("空 context tokenInfo 应零值")
	}
}

func TestListEntriesEmptyAndCorrupt(t *testing.T) {
	fx := kdbtest.New(t)
	s := &Server{Store: fx.Store}
	w := &respRecorder{header: http.Header{}}
	s.ListEntries(w, nil)
	if w.code != 200 {
		t.Fatalf("空库列表应 200，实际 %d", w.code)
	}
	var list []json.RawMessage
	if json.Unmarshal([]byte(w.buf.String()), &list) != nil || len(list) != 0 {
		t.Fatalf("空库应返回 []: %s", w.buf.String())
	}
}

type respRecorder struct {
	header http.Header
	code   int
	buf    strings.Builder
}

func (c *respRecorder) Header() http.Header       { return c.header }
func (c *respRecorder) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = 200
	}
	return c.buf.Write(b)
}
func (c *respRecorder) WriteHeader(code int) { c.code = code }

// 关库直调：列表 500、单条 404、搜索 500
func TestClosedDBHandlers(t *testing.T) {
	fx := kdbtest.New(t)
	s := &Server{Store: fx.Store}
	fx.DB.Close()
	w := &respRecorder{header: http.Header{}}
	s.ListEntries(w, nil)
	if w.code != 500 {
		t.Fatalf("关库列表应 500，实际 %d", w.code)
	}
	if !strings.Contains(w.buf.String(), "服务器内部错误") {
		t.Fatalf("500 应回通用文案而非内部错误原文，实际 %q", w.buf.String())
	}
	w2 := &respRecorder{header: http.Header{}}
	r2, _ := http.NewRequest("GET", "/entries/1", nil)
	s.GetEntry(w2, r2)
	if w2.code != 404 {
		t.Fatalf("关库单条应 404，实际 %d", w2.code)
	}
	w3 := &respRecorder{header: http.Header{}}
	r3, _ := http.NewRequest("GET", "/search?q=x", nil)
	s.Search(w3, r3)
	if w3.code != 500 {
		t.Fatalf("关库搜索应 500，实际 %d", w3.code)
	}
	if !strings.Contains(w3.buf.String(), "服务器内部错误") {
		t.Fatalf("500 应回通用文案而非内部错误原文，实际 %q", w3.buf.String())
	}
	w4 := &respRecorder{header: http.Header{}}
	r4, _ := http.NewRequest("POST", "/entries/1/reveal", strings.NewReader(`{"field":"f"}`))
	s.Reveal(w4, r4)
	if w4.code != 404 {
		t.Fatalf("关库 reveal 应 404，实际 %d", w4.code)
	}
}
