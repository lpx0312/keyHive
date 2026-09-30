package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"keyhive/internal/audit"
	"keyhive/internal/auth"
	"keyhive/internal/model"
)

// ---- 模板管理 ----

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.Template{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var t model.Template
	if err := readBody(r, &t); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if err := s.Store.SaveTemplate(&t); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, t)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTemplate(parseInt64(chi.URLParam(r, "id")))
	if err != nil {
		writeErr(w, 404, "模板不存在")
		return
	}
	var in model.Template
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if strings.TrimSpace(in.Name) != "" {
		t.Name = strings.TrimSpace(in.Name)
	}
	if strings.TrimSpace(in.Group) != "" {
		t.Group = strings.TrimSpace(in.Group)
	}
	if in.Fields != nil {
		t.Fields = in.Fields
	}
	if err := s.Store.SaveTemplate(t); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	t, err := s.Store.GetTemplate(id)
	if err != nil {
		writeErr(w, 404, "模板不存在")
		return
	}
	if t.Builtin {
		writeErr(w, 400, "内置模板不可删除")
		return
	}
	if err := s.Store.DeleteTemplate(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionTemplateDel, nil,
		`{"template":"`+t.Name+`"}`, clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- AI 令牌管理 ----

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := auth.ListAPITokens(s.Store.DB)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.APIToken{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt *string  `json:"expires_at"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, 400, "令牌名不能为空")
		return
	}
	// scope 白名单；search 隐含 read
	valid := map[string]bool{"read": true, "search": true, "reveal": true}
	scopes := []string{"read"}
	seen := map[string]bool{"read": true}
	for _, sc := range req.Scopes {
		if valid[sc] && !seen[sc] {
			scopes = append(scopes, sc)
			seen[sc] = true
		}
	}
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ExpiresAt)); err != nil {
			writeErr(w, 400, "过期时间格式应为 RFC3339，如 2027-01-01T00:00:00Z")
			return
		}
		exp := strings.TrimSpace(*req.ExpiresAt)
		req.ExpiresAt = &exp
	} else {
		req.ExpiresAt = nil
	}
	t, plaintext, err := auth.CreateAPIToken(s.Store.DB, req.Name, scopes, req.ExpiresAt)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionTokenCreate, nil,
		`{"name":"`+req.Name+`","scopes":`+joinScopes(scopes)+`}`, clientIP(r))
	t.Token = plaintext
	writeJSON(w, 201, t)
}

func joinScopes(scopes []string) string {
	out := "["
	for i, s := range scopes {
		if i > 0 {
			out += ","
		}
		out += `"` + s + `"`
	}
	return out + "]"
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	if err := auth.RevokeAPIToken(s.Store.DB, id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	eid := id
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionTokenRevoke, &eid, `{}`, clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- 审计 ----

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := intQuery(q.Get("limit"), 50), intQuery(q.Get("offset"), 0)
	list, err := audit.List(s.Store.DB, q.Get("action"), limit, offset)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.AuditLog{}
	}
	writeJSON(w, 200, list)
}

func intQuery(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return def
	}
	return n
}
