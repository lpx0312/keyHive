package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/lpx0312/keyHive/internal/audit"
	"github.com/lpx0312/keyHive/internal/auth"
	"github.com/lpx0312/keyHive/internal/model"
	"github.com/lpx0312/keyHive/internal/store"
)

// Server 人用 API（session cookie 认证）
type Server struct {
	Store *store.Store
}

// maxBodyBytes 全站请求体大小上限（含未认证端点，防未登录内存耗尽）
const maxBodyBytes = 10 << 20 // 10MB

// LimitBody 统一给请求体套 MaxBytesReader：Content-Length 已超限直接 413，
// 运行中（如 chunked）读取超限由 readBody 识别后同样映射 413。
// 导出供 main 在顶层 router 挂载，使 /api/v1/ai（Bearer token AI API）子树
// 也纳入限制；人用 Routes() 内的挂载保留（Routes 会被单独挂载使用），
// 两处同限值 10MB 双重包裹行为等价、无害。
func LimitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBodyBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, "请求体超过 10MB 上限")
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeInternalErr 5xx 内部错误统一出口：完整错误只进服务端日志，客户端只回通用文案
func writeInternalErr(w http.ResponseWriter, err error) {
	log.Printf("[api] internal error: %v", err)
	writeErr(w, 500, "服务器内部错误")
}

// errBodyTooLarge 请求体超限哨兵（readBody 识别 MaxBytesError 后返回）
var errBodyTooLarge = errors.New("http: request body too large")

// writeReadErr 请求体读取/解码失败统一出口：超限 413，其余按格式错误 400
func writeReadErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errBodyTooLarge) {
		writeErr(w, http.StatusRequestEntityTooLarge, "请求体超过 10MB 上限")
		return
	}
	writeErr(w, 400, "请求格式错误")
}

func readBody(r *http.Request, v any) error {
	defer r.Body.Close()
	err := json.NewDecoder(r.Body).Decode(v)
	// MaxBytesReader 超限读取返回 *http.MaxBytesError，与普通解码错误区分开
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return errBodyTooLarge
	}
	return err
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
	// 全站请求体大小限制（含未认证端点）
	r.Use(LimitBody)
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

		// AI 录入助手：聊天对所有登录用户开放；LLM 配置读取登录即可，
		// 但测试连接 / 拉模型会解密已保存 Key 并向用户自填地址发起外联请求，仅管理员可用
		pr.Get("/ai-config", s.handleAIConfig)
		pr.Post("/ai-chat", s.handleAIChat)
		pr.Post("/ai-chat/stream", s.handleAIChatStream)
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
		ar.Post("/ai-config/test", s.handleAIConfigTest)
		ar.Post("/ai-config/models", s.handleAIListModels)
	})
}

// exportEntries 全库导出（admin，每次记审计）：默认明文；
// ?masked=1（或 masked=true）时敏感字段值遮蔽为 ***，其余结构不变
func (s *Server) exportEntries(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEntries("", "")
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	masked := r.URL.Query().Get("masked") == "1" || r.URL.Query().Get("masked") == "true"
	if masked {
		for i := range list {
			list[i].MaskSecrets()
		}
	}
	uid, uname := s.actor(r)
	audit.Log(s.Store.DB, "user", uid, uname, model.ActionExport, nil,
		audit.Detail(map[string]any{"count": len(list), "masked": masked}), clientIP(r))
	writeJSON(w, 200, list)
}

// ---- auth ----

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readBody(r, &req); err != nil {
		writeReadErr(w, err)
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
		writeReadErr(w, err)
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
		writeInternalErr(w, err)
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
		writeReadErr(w, err)
		return
	}
	if err := s.Store.CreateEntry(&e); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	id := e.ID
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionEntryCreate, &id,
		audit.Detail(map[string]any{"title": e.Title}), clientIP(r))
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
			audit.Detail(map[string]any{"title": e.Title}), clientIP(r))
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
		writeReadErr(w, err)
		return
	}
	if err := s.Store.UpdateEntry(id, &e); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	eid := id
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionEntryUpdate, &eid,
		audit.Detail(map[string]any{"title": e.Title}), clientIP(r))
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
		writeInternalErr(w, err)
		return
	}
	uid, _ := auth.UserID(r.Context())
	eid := id
	audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionEntryDelete, &eid,
		audit.Detail(map[string]any{"title": e.Title}), clientIP(r))
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
		audit.Detail(map[string]any{"template": t.Name}), clientIP(r))
	writeJSON(w, 201, t)
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Categories()
	if err != nil {
		writeInternalErr(w, err)
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
