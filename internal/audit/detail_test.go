package audit

import (
	"encoding/json"
	"math"
	"testing"
)

// Detail 必须产出合法 JSON：标题含双引号/反斜杠等字符时手工拼接会产出坏 JSON
func TestDetail(t *testing.T) {
	// map：含双引号、反斜杠、中文 → 可 Unmarshal 且值还原
	raw := `生产"ACR"\镜像库`
	s := Detail(map[string]any{"title": raw, "n": 2})
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("map detail 应为合法 JSON: %q %v", s, err)
	}
	if m["title"] != raw || m["n"] != float64(2) {
		t.Fatalf("map detail 往返不一致: %q → %v", raw, m)
	}

	// struct：同样可还原
	type detail struct {
		Title   string `json:"title"`
		EntryID int64  `json:"entry_id"`
	}
	s2 := Detail(detail{Title: `路径C:\Users\"x"`, EntryID: 7})
	var d detail
	if err := json.Unmarshal([]byte(s2), &d); err != nil {
		t.Fatalf("struct detail 应为合法 JSON: %q %v", s2, err)
	}
	if d.Title != `路径C:\Users\"x"` || d.EntryID != 7 {
		t.Fatalf("struct detail 往返不一致: %+v", d)
	}

	// nil → json 字面量 null（合法 JSON，可 Unmarshal）
	if s3 := Detail(nil); s3 != "null" {
		t.Fatalf("nil 应序列化为 null: %q", s3)
	}
	var v any
	if err := json.Unmarshal([]byte(Detail(nil)), &v); err != nil || v != nil {
		t.Fatalf("null 应可 Unmarshal 为 nil: %v %v", v, err)
	}

	// 失败类型（NaN / channel）→ 兜底 "{}"
	for i, bad := range []any{math.NaN(), make(chan int)} {
		if s := Detail(bad); s != "{}" {
			t.Fatalf("case %d 失败类型应兜底 {}: %q", i, s)
		}
	}
}

// Log + Detail 全链路：含特殊字符的 detail 原样落库、原样读出（审计取证契约）
func TestLogDetailRoundTrip(t *testing.T) {
	db := newDB(t)
	detail := Detail(map[string]any{"title": `带"引号"与\反斜杠`})
	Log(db, "user", 1, "admin", "entry_update", nil, detail, "1.1.1.1")

	list, err := List(db, "entry_update", 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("应 1 条: %v %d", err, len(list))
	}
	if list[0].Detail != detail {
		t.Fatalf("detail 应原样落库: %q != %q", list[0].Detail, detail)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(list[0].Detail), &m); err != nil {
		t.Fatalf("落库 detail 应仍为合法 JSON: %v", err)
	}
	if m["title"] != `带"引号"与\反斜杠` {
		t.Fatalf("detail 往返不一致: %v", m)
	}
}

// limit 封顶边界：>200 与 <=0 收敛 50；恰好 200 放行；action 筛选与封顶叠加；负 offset 按 0
func TestListLimitBoundary(t *testing.T) {
	db := newDB(t)
	for i := 0; i < 55; i++ {
		Log(db, "user", 1, "u", "login", nil, "", "")
	}
	for i := 0; i < 5; i++ {
		Log(db, "user", 1, "u", "export", nil, "", "")
	}

	// 恰好 200：不封顶，返回全部 60（若被误收敛为 50 只会见到 50 条）
	list, err := List(db, "", 200, 0)
	if err != nil || len(list) != 60 {
		t.Fatalf("limit=200 应返回 60 条: %v %d", err, len(list))
	}
	// 0 / 负数 / 201：都收敛默认 50（库存 55 > 50，可区分）
	for _, lim := range []int{0, -1, 201, 9999} {
		list, err := List(db, "", lim, 0)
		if err != nil || len(list) != 50 {
			t.Fatalf("limit=%d 应按 50 处理返回 50 条: %v %d", lim, err, len(list))
		}
	}
	// action 筛选叠加封顶：export 仅 5 条不受影响，login 55 条被 201→50 截断
	if list, _ := List(db, "export", 0, 0); len(list) != 5 {
		t.Fatalf("export 筛选应 5 条: %d", len(list))
	}
	if list, _ := List(db, "login", 201, 0); len(list) != 50 {
		t.Fatalf("login + limit=201 应收敛 50: %d", len(list))
	}
	if n, _ := Count(db, "login"); n != 55 {
		t.Fatalf("Count(login) 应 55（与 List 口径一致的前提）: %d", n)
	}
	// 负 offset：SQLite 按 0 处理 → 仍从最新一条开始
	list, _ = List(db, "", 10, -3)
	if len(list) != 10 {
		t.Fatalf("负 offset 应按 0 处理返回 10 条: %d", len(list))
	}
	if list[0].ID != 60 {
		t.Fatalf("负 offset 应视为 0，首条应为最新 ID=60: %d", list[0].ID)
	}
}
