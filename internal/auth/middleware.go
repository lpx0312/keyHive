package auth

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"strings"
)

const (
	CookieName = "keyhive_session"
	CtxUserKey ctxKey = iota
	CtxTokenKey
)

type ctxKey int

// UserID 从 context 取登录用户
func UserID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(CtxUserKey).(int64)
	return id, ok
}

// RequireAdmin 管理员专用端点（叠加在 RequireSession 之后）
func RequireAdmin(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, ok := UserID(r.Context())
			if !ok {
				http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
				return
			}
			var isAdmin, disabled int
			err := db.QueryRow(`SELECT is_admin, disabled FROM users WHERE id = ?`, uid).Scan(&isAdmin, &disabled)
			if err != nil || disabled == 1 {
				http.Error(w, `{"error":"账号不可用"}`, http.StatusUnauthorized)
				return
			}
			if isAdmin != 1 {
				http.Error(w, `{"error":"需要管理员权限"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TokenInfo 从 context 取 AI 令牌信息（id, name, scopes）
func TokenInfo(ctx context.Context) (int64, string, []string, bool) {
	t, ok := ctx.Value(CtxTokenKey).(*apiTokenCtx)
	if !ok {
		return 0, "", nil, false
	}
	return t.ID, t.Name, t.Scopes, true
}

type apiTokenCtx struct {
	ID     int64
	Name   string
	Scopes []string
}

func (t *apiTokenCtx) Has(s string) bool {
	for _, v := range t.Scopes {
		if v == s {
			return true
		}
	}
	return false
}

// RequireSession 人用 API 认证
func RequireSession(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(CookieName)
			if err != nil || c.Value == "" {
				http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
				return
			}
			sess, err := GetSession(db, c.Value)
			if err != nil {
				log.Printf("session 校验失败: %v", err)
				http.Error(w, `{"error":"会话已过期"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), CtxUserKey, sess.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireToken AI 用 API 认证 + scope 校验
func RequireToken(db *sql.DB, scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, `{"error":"缺少 Bearer 令牌"}`, http.StatusUnauthorized)
				return
			}
			t, err := GetAPIToken(db, strings.TrimPrefix(h, "Bearer "))
			if err != nil {
				http.Error(w, `{"error":"令牌无效、已吊销或已过期"}`, http.StatusUnauthorized)
				return
			}
			if scope != "" && !t.HasScope(scope) {
				http.Error(w, `{"error":"令牌缺少权限: `+scope+`"}`, http.StatusForbidden)
				return
			}
			TouchAPIToken(db, t.ID)
			ctx := context.WithValue(r.Context(), CtxTokenKey, &apiTokenCtx{
				ID: t.ID, Name: t.Name, Scopes: t.Scopes})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
