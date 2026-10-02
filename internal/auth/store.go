package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/lpx0312/keyHive/internal/model"
)

const SessionTTL = 7 * 24 * time.Hour

type SessionRecord struct {
	ID        string // 会话哈希（库里只存它，明文只在 cookie）
	UserID    int64
	IP        string
	UserAgent string
	CreatedAt string
	ExpiresAt string
	LastSeen  string
}

// RandomToken 生成 URL 安全随机令牌
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// ---- Session ----

// CreateSession 登录成功后创建会话，返回明文 cookie 值（库里只存哈希）
func CreateSession(db *sql.DB, userID int64, ip, userAgent string) (string, *SessionRecord, error) {
	token, err := RandomToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	rec := &SessionRecord{
		UserID:    userID,
		IP:        ip,
		UserAgent: userAgent,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(SessionTTL).Format(time.RFC3339),
		LastSeen:  now.Format(time.RFC3339),
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, user_id, ip, user_agent, created_at, expires_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(token), userID, ip, userAgent, rec.CreatedAt, rec.ExpiresAt, rec.LastSeen); err != nil {
		return "", nil, err
	}
	rec.ID = hashToken(token)
	return token, rec, nil
}

var ErrSessionInvalid = errors.New("会话无效或已过期")

// GetSession 校验并返回会话，滑动续期
func GetSession(db *sql.DB, token string) (*SessionRecord, error) {
	if token == "" {
		return nil, ErrSessionInvalid
	}
	row := db.QueryRow(`SELECT id, user_id, ip, user_agent, created_at, expires_at, last_seen_at
		FROM sessions WHERE id = ?`, hashToken(token))
	var r SessionRecord
	err := row.Scan(&r.ID, &r.UserID, &r.IP, &r.UserAgent, &r.CreatedAt, &r.ExpiresAt, &r.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339, r.ExpiresAt)
	if err != nil || time.Now().UTC().After(exp) {
		return nil, ErrSessionInvalid
	}
	newExp := time.Now().UTC().Add(SessionTTL).Format(time.RFC3339)
	if _, err = db.Exec(`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		nowUTC(), newExp, hashToken(token)); err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteSession 登出
func DeleteSession(db *sql.DB, token string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE id = ?`, hashToken(token))
	return err
}

// DeleteUserSessions 踢掉该用户所有会话
func DeleteUserSessions(db *sql.DB, userID int64) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// ---- API Token（AI 用）----

const tokenPrefix = "kh_"

// CreateAPIToken 创建 AI 令牌，明文仅此一次返回
func CreateAPIToken(db *sql.DB, name string, scopes []string, expiresAt *string) (*model.APIToken, string, error) {
	raw, err := RandomToken()
	if err != nil {
		return nil, "", err
	}
	plaintext := tokenPrefix + raw
	scopesJSON, _ := json.Marshal(scopes)
	now := nowUTC()
	res, err := db.Exec(
		`INSERT INTO api_tokens (name, token_hash, scopes, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		name, hashToken(plaintext), string(scopesJSON), now, expiresNil(expiresAt))
	if err != nil {
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	t := &model.APIToken{ID: id, Name: name, Scopes: scopes, CreatedAt: now, ExpiresAt: expiresAt}
	return t, plaintext, nil
}

func expiresNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

var ErrTokenInvalid = errors.New("令牌无效、已吊销或已过期")

// GetAPIToken 校验 Bearer 令牌并返回记录（不更新 last_used，由调用方按需更新）
func GetAPIToken(db *sql.DB, plaintext string) (*model.APIToken, error) {
	if plaintext == "" {
		return nil, ErrTokenInvalid
	}
	row := db.QueryRow(
		`SELECT id, name, scopes, created_at, last_used_at, expires_at, revoked_at
		 FROM api_tokens WHERE token_hash = ?`, hashToken(plaintext))
	var t model.APIToken
	var scopesJSON string
	var lastUsed, expires, revoked sql.NullString
	err := row.Scan(&t.ID, &t.Name, &scopesJSON, &t.CreatedAt, &lastUsed, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	if revoked.Valid {
		return nil, ErrTokenInvalid
	}
	if expires.Valid {
		exp, err := time.Parse(time.RFC3339, expires.String)
		if err != nil || time.Now().UTC().After(exp) {
			return nil, ErrTokenInvalid
		}
	}
	json.Unmarshal([]byte(scopesJSON), &t.Scopes)
	if lastUsed.Valid {
		lu := lastUsed.String
		t.LastUsedAt = &lu
	}
	if expires.Valid {
		e := expires.String
		t.ExpiresAt = &e
	}
	return &t, nil
}

// TouchAPIToken 更新最后使用时间
func TouchAPIToken(db *sql.DB, id int64) {
	db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, nowUTC(), id)
}

// RevokeAPIToken 吊销
func RevokeAPIToken(db *sql.DB, id int64) error {
	_, err := db.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, nowUTC(), id)
	return err
}

// ListAPITokens 全量列表（不含哈希）
func ListAPITokens(db *sql.DB) ([]model.APIToken, error) {
	rows, err := db.Query(
		`SELECT id, name, scopes, created_at, last_used_at, expires_at, revoked_at
		 FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.APIToken
	for rows.Next() {
		var t model.APIToken
		var scopesJSON string
		var lastUsed, expires, revoked sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &scopesJSON, &t.CreatedAt, &lastUsed, &expires, &revoked); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(scopesJSON), &t.Scopes)
		if lastUsed.Valid {
			lu := lastUsed.String
			t.LastUsedAt = &lu
		}
		if expires.Valid {
			e := expires.String
			t.ExpiresAt = &e
		}
		if revoked.Valid {
			r := revoked.String
			t.RevokedAt = &r
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
