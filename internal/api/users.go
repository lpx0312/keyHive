package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"keyhive/internal/audit"
	"keyhive/internal/auth"
	"keyhive/internal/model"
)

func dbNow() string { return time.Now().UTC().Format(time.RFC3339) }

// countAdmins 现存有效管理员数（未禁用）
func countAdmins(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1 AND disabled = 0`).Scan(&n)
	return n, err
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.DB.Query(
		`SELECT id, username, is_admin, disabled, created_at FROM users ORDER BY id`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	list := []model.User{}
	for rows.Next() {
		var u model.User
		var isAdmin, disabled int
		if err := rows.Scan(&u.ID, &u.Username, &isAdmin, &disabled, &u.CreatedAt); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		u.IsAdmin, u.Disabled = isAdmin == 1, disabled == 1
		list = append(list, u)
	}
	writeJSON(w, 200, list)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		writeErr(w, 400, "用户名不能为空")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, 400, "密码至少 8 位")
		return
	}
	var exists int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, req.Username).Scan(&exists)
	if exists > 0 {
		writeErr(w, 400, "用户名已存在")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, 500, "密码哈希失败")
		return
	}
	now := dbNow()
	isAdmin := 0
	if req.IsAdmin {
		isAdmin = 1
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		req.Username, hash, isAdmin, now, now)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	uid, uname := s.actor(r)
	audit.Log(s.Store.DB, "user", uid, uname, model.ActionUserCreate, nil,
		`{"username":"`+req.Username+`","is_admin":`+boolJSON(req.IsAdmin)+`}`, clientIP(r))
	writeJSON(w, 201, model.User{ID: id, Username: req.Username, IsAdmin: req.IsAdmin, CreatedAt: now})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	var target model.User
	var isAdmin, disabled int
	err := s.Store.DB.QueryRow(
		`SELECT id, username, is_admin, disabled FROM users WHERE id = ?`, id).
		Scan(&target.ID, &target.Username, &isAdmin, &disabled)
	if err != nil {
		writeErr(w, 404, "用户不存在")
		return
	}
	target.IsAdmin, target.Disabled = isAdmin == 1, disabled == 1

	var req struct {
		Password *string `json:"password"`
		Disabled *bool   `json:"disabled"`
		IsAdmin  *bool   `json:"is_admin"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}

	// 护栏：目标当前是唯一有效管理员时，禁用/降权被拒
	if target.IsAdmin && !target.Disabled {
		losingAdmin := (req.IsAdmin != nil && !*req.IsAdmin) || (req.Disabled != nil && *req.Disabled)
		if losingAdmin {
			if n, err := countAdmins(s.Store.DB); err == nil && n <= 1 {
				writeErr(w, 400, "不能禁用或降级最后一个管理员")
				return
			}
		}
	}

	sets := []string{}
	args := []any{}
	if req.Password != nil {
		if len(*req.Password) < 8 {
			writeErr(w, 400, "密码至少 8 位")
			return
		}
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			writeErr(w, 500, "密码哈希失败")
			return
		}
		sets = append(sets, "password_hash = ?")
		args = append(args, hash)
	}
	if req.Disabled != nil {
		sets = append(sets, "disabled = ?")
		v := 0
		if *req.Disabled {
			v = 1
		}
		args = append(args, v)
	}
	if req.IsAdmin != nil {
		sets = append(sets, "is_admin = ?")
		v := 0
		if *req.IsAdmin {
			v = 1
		}
		args = append(args, v)
	}
	if len(sets) == 0 {
		writeErr(w, 400, "没有需要更新的内容")
		return
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, dbNow(), id)
	if _, err := s.Store.DB.Exec(`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	// 禁用或改密后踢掉该用户全部会话（改密含本人时前端会引导重新登录）
	if req.Disabled != nil || req.Password != nil {
		auth.DeleteUserSessions(s.Store.DB, id)
	}

	uid, uname := s.actor(r)
	audit.Log(s.Store.DB, "user", uid, uname, model.ActionUserUpdate, &id,
		`{"username":"`+target.Username+`","reset_password":`+boolJSON(req.Password != nil)+
			`,"disabled":`+boolPtrJSON(req.Disabled)+`,"is_admin":`+boolPtrJSON(req.IsAdmin)+`}`, clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	uid, _ := s.actor(r)
	if id == uid {
		writeErr(w, 400, "不能删除自己")
		return
	}
	var target model.User
	var isAdmin, disabled int
	err := s.Store.DB.QueryRow(
		`SELECT id, username, is_admin, disabled FROM users WHERE id = ?`, id).
		Scan(&target.ID, &target.Username, &isAdmin, &disabled)
	if err != nil {
		writeErr(w, 404, "用户不存在")
		return
	}
	target.IsAdmin, target.Disabled = isAdmin == 1, disabled == 1
	if target.IsAdmin && !target.Disabled {
		if n, err := countAdmins(s.Store.DB); err == nil && n <= 1 {
			writeErr(w, 400, "不能删除最后一个管理员")
			return
		}
	}
	if _, err := s.Store.DB.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	auth.DeleteUserSessions(s.Store.DB, id)
	actorID, uname := s.actor(r)
	audit.Log(s.Store.DB, "user", actorID, uname, model.ActionUserDelete, &id,
		`{"username":"`+target.Username+`"}`, clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// actor 当前登录用户（id + 用户名）
func (s *Server) actor(r *http.Request) (int64, string) {
	uid, _ := auth.UserID(r.Context())
	return uid, s.usernameByID(uid)
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func boolPtrJSON(b *bool) string {
	if b == nil {
		return "null"
	}
	return boolJSON(*b)
}
