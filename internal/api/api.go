package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"keyhive/internal/audit"
	"keyhive/internal/auth"
	"keyhive/internal/model"
	"keyhive/internal/store"
)

// Server 人用 API（session cookie 认证）
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

func readBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func clientIP(r *http.Request) string {
	// 内网部署，直接取 RemoteAddr 即可；有反代时可配 X-Forwarded-For
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return strings.TrimSpace(strings.Split(xf, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func (s *Server) usernameByID(id int64) string {
	var name string
	s.Store.DB.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&name)
	return name
}

// Routes 挂载全部人用路由
func (s *Server) Routes(r chi.Router) {
	r.Post("/auth/login", s.login)
	r.Post("/auth/logout", s.logout)
	r.Get("/version", s.versionInfo)

	r.Group(func(pr chi.Router) {
		pr.Use(auth.RequireSession(s.Store.DB))

		pr.Get("/auth/me", s.me)
		pr.Post("/auth/logout-all", s.logoutAll)
		pr.Post("/password", s.changePassword)

		pr.Get("/entries", s.listEntries)
		pr.Post("/entries", s.createEntry)
		pr.Get("/entries/{id}", s.getEntry)
		pr.Put("/entries/{id}", s.updateEntry)
		pr.Delete("/entries/{id}", s.deleteEntry)
		pr.Post("/entries/{id}/save-as-template", s.saveAsTemplate)
		pr.Get("/categories", s.categories)
		pr.Post("/entries/{id}/totp", s.genTOTP)
		pr.Get("/settings/stale-days", s.getStaleDays)

		pr.Get("/templates", s.listTemplates)
		pr.Post("/templates", s.createTemplate)
		pr.Put("/templates/{id}", s.updateTemplate)
		pr.Delete("/templates/{id}", s.deleteTemplate)

		// AI 录入助手：聊天对所有登录用户开放，LLM 配置仅管理员
		pr.Get("/ai-config", s.handleAIConfig)
		pr.Post("/ai-config/test", s.handleAIConfigTest)
		pr.Post("/ai-chat", s.handleAIChat)
	})

	// 管理员：用户管理、AI 令牌、审计日志
	r.Group(func(ar chi.Router) {
		ar.Use(auth.RequireSession(s.Store.DB))
		ar.Use(auth.RequireAdmin(s.Store.DB))

		ar.Get("/users", s.listUsers)
		ar.Post("/users", s.createUser)
		ar.Put("/users/{id}", s.updateUser)
		ar.Delete("/users/{id}", s.deleteUser)

		ar.Get("/tokens", s.listTokens)
		ar.Post("/tokens", s.createToken)
		ar.Delete("/tokens/{id}", s.revokeToken)

		ar.Get("/audit", s.listAudit)
		ar.Get("/export", s.exportEntries)
		ar.Post("/rotate-key", s.rotateKey)
		ar.Post("/import", s.importCSV)
		ar.Put("/settings/stale-days", s.putStaleDays)
		ar.Put("/ai-config", s.handleAIConfig)
	})
}

// exportEntries 全库明文导出（admin，每次记审计）
func (s *Server) exportEntries(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEntries("", "")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid, uname := s.actor(r)
	audit.Log(s.Store.DB, "user", uid, uname, model.ActionExport, nil,
		fmt.Sprintf(`{"count":%d}`, len(list)), clientIP(r))
	writeJSON(w, 200, list)
}

// ---- auth ----

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	var id int64
	var hash string
	var disabled int
	err := s.Store.DB.QueryRow(`SELECT id, password_hash, disabled FROM users WHERE username = ?`,
		strings.TrimSpace(req.Username)).Scan(&id, &hash, &disabled)
	ip := clientIP(r)
	if err != nil || !auth.VerifyPassword(req.Password, hash) {
		audit.Log(s.Store.DB, "user", 0, strings.TrimSpace(req.Username),
			model.ActionLoginFailed, nil, `{}`, ip)
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	if disabled == 1 {
		audit.Log(s.Store.DB, "user", id, req.Username, model.ActionLoginFailed, nil,
			`{"reason":"账号已禁用"}`, ip)
		writeErr(w, 403, "账号已被禁用，请联系管理员")
		return
	}
	token, _, err := auth.CreateSession(s.Store.DB, id, ip, r.UserAgent())
	if err != nil {
		writeErr(w, 500, "创建会话失败")
		return
	}
	audit.Log(s.Store.DB, "user", id, req.Username, model.ActionLogin, nil, `{}`, ip)
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, 200, map[string]string{"username": strings.TrimSpace(req.Username)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		auth.DeleteSession(s.Store.DB, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	auth.DeleteUserSessions(s.Store.DB, uid)
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	uid, ok := auth.UserID(r.Context())
	if !ok {
		writeErr(w, 401, "未登录")
		return
	}
	var username string
	var isAdmin int
	s.Store.DB.QueryRow(`SELECT username, is_admin FROM users WHERE id = ?`, uid).Scan(&username, &isAdmin)
	writeJSON(w, 200, map[string]any{"id": uid, "username": username, "is_admin": isAdmin == 1})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	uid, _ := auth.UserID(r.Context())
	var hash string
	var username string
	s.Store.DB.QueryRow(`SELECT username, password_hash FROM users WHERE id = ?`, uid).Scan(&username, &hash)
	if !auth.VerifyPassword(req.OldPassword, hash) {
		writeErr(w, 400, "旧密码错误")
		return
	}
	if len(req.NewPassword) < 8 {
		writeErr(w, 400, "新密码至少 8 位")
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, 500, "哈希失败")
		return
	}
	if _, err := s.Store.DB.Exec(`UPDATE users SET password_hash=?, updated_at=datetime('now') WHERE id=?`,
		newHash, uid); err != nil {
		writeErr(w, 500, "更新失败")
		return
	}
	audit.Log(s.Store.DB, "user", uid, username, model.ActionPassword, nil, `{}`, clientIP(r))
	// 改密后踢掉全部会话，强制重新登录
	auth.DeleteUserSessions(s.Store.DB, uid)
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- entries ----

func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEntries(r.URL.Query().Get("q"), r.URL.Query().Get("category"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range list {
		list[i].MaskSecrets()
	}
	writeJSON(w, 200, list)
}

func (s *Server) createEntry(w http.ResponseWriter, r *http.Request) {
	var e model.Entry
	if err := readBody(r, &e); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if err := s.Store.CreateEntry(&e); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	id := e.ID
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionEntryCreate, &id,
		`{"title":"`+e.Title+`"}`, clientIP(r))
	e.MaskSecrets()
	writeJSON(w, 201, e)
}

func (s *Server) getEntry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	e, err := s.Store.GetEntry(parseInt64(id))
	if err != nil {
		writeErr(w, 404, "条目不存在")
		return
	}
	uid, _ := auth.UserID(r.Context())
	eid := e.ID
	if r.URL.Query().Get("reveal") == "true" {
		audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionEntryReveal, &eid,
			`{"title":"`+e.Title+`"}`, clientIP(r))
		writeJSON(w, 200, e) // 明文
		return
	}
	e.MaskSecrets()
	writeJSON(w, 200, e)
}

func (s *Server) updateEntry(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	var e model.Entry
	if err := readBody(r, &e); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if err := s.Store.UpdateEntry(id, &e); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	eid := id
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionEntryUpdate, &eid,
		`{"title":"`+e.Title+`"}`, clientIP(r))
	e.MaskSecrets()
	writeJSON(w, 200, e)
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	e, err := s.Store.GetEntry(id)
	if err != nil {
		writeErr(w, 404, "条目不存在")
		return
	}
	if err := s.Store.DeleteEntry(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	eid := id
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionEntryDelete, &eid,
		`{"title":"`+e.Title+`"}`, clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// saveAsTemplate 从条目另存为模板（只带走骨架与注释，不带值）
func (s *Server) saveAsTemplate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	e, err := s.Store.GetEntry(id)
	if err != nil {
		writeErr(w, 404, "条目不存在")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		req.Name = e.Title + " 模板"
	}
	t := &model.Template{
		Name:   strings.TrimSpace(req.Name),
		Category: e.Category + "_tpl",
		Group:  "我的模板",
		Fields: e.Fields,
	}
	if err := s.Store.SaveTemplate(t); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	eid := e.ID
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionTemplateSave, &eid,
		`{"template":"`+t.Name+`"}`, clientIP(r))
	writeJSON(w, 201, t)
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Categories()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []string{}
	}
	writeJSON(w, 200, list)
}

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
