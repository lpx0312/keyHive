package aiapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"keyhive/internal/audit"
	"keyhive/internal/auth"
	"keyhive/internal/model"
	"keyhive/internal/store"
)

// Server AI 用 API（Bearer token，scope 分级：read / search / reveal）
type Server struct {
	Store *store.Store
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// 各端点由 main 按不同 scope 挂载：
// read → ListEntries/GetEntry；search → Search；reveal → Reveal

func parseInt64(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func (s *Server) tokenInfo(r *http.Request) (int64, string) {
	id, name, _, ok := auth.TokenInfo(r.Context())
	if !ok {
		return 0, ""
	}
	return id, name
}

// listEntries 返回 AI 可见条目（ai_visible=false 完全隐身），敏感值遮蔽，注释原样
func (s *Server) ListEntries(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEntries("", "")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]model.Entry, 0, len(list))
	for _, e := range list {
		if !e.AIVisible {
			continue
		}
		e.MaskSecrets()
		out = append(out, e)
	}
	writeJSON(w, 200, out)
}

func (s *Server) GetEntry(w http.ResponseWriter, r *http.Request) {
	e, err := s.Store.GetEntry(parseInt64(chi.URLParam(r, "id")))
	if err != nil || !e.AIVisible {
		writeErr(w, 404, "条目不存在")
		return
	}
	e.MaskSecrets()
	writeJSON(w, 200, e)
}

func (s *Server) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, 400, "缺少 q 参数")
		return
	}
	// 复用 ListEntries 的模糊匹配（title/category/description）
	list, err := s.Store.ListEntries(q, "")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]model.Entry, 0, len(list))
	for _, e := range list {
		if !e.AIVisible {
			continue
		}
		e.MaskSecrets()
		out = append(out, e)
	}
	writeJSON(w, 200, out)
}

// reveal 取单个敏感字段明文（需 reveal scope），全程审计
func (s *Server) Reveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Field string `json:"field"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Field) == "" {
		writeErr(w, 400, `请求体应为 {"field":"字段名"}`)
		return
	}
	e, err := s.Store.GetEntry(parseInt64(chi.URLParam(r, "id")))
	if err != nil || !e.AIVisible {
		writeErr(w, 404, "条目不存在")
		return
	}
	f := e.FieldByKey(strings.TrimSpace(req.Field))
	if f == nil {
		writeErr(w, 404, "字段不存在: "+req.Field)
		return
	}

	actorID, actorName := s.tokenInfo(r)
	eid := e.ID
	audit.Log(s.Store.DB, "token", actorID, actorName, model.ActionFieldReveal, &eid,
		`{"title":"`+e.Title+`","field":"`+f.Key+`"}`, r.RemoteAddr)

	writeJSON(w, 200, map[string]any{
		"entry_id":    e.ID,
		"entry_title": e.Title,
		"key":         f.Key,
		"description": f.Description,
		"value":       f.Value,
	})
}
