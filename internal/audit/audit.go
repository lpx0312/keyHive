package audit

import (
	"database/sql"
	"encoding/json"
	"github.com/lpx0312/keyHive/internal/model"
	"time"
)

// Detail 用 json.Marshal 安全构造审计 detail：标题/字段名等含双引号、反斜杠时
// 字符串拼接会产出非法 JSON，破坏审计的取证契约，调用方一律改用本函数
func Detail(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// Log 写一条审计记录（失败只忽略，不影响主流程）
func Log(db *sql.DB, actorType string, actorID int64, actorName, action string, entryID *int64, detail, ip string) {
	if detail == "" {
		detail = "{}"
	}
	db.Exec(`INSERT INTO audit_logs (actor_type, actor_id, actor_name, action, entry_id, detail, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		actorType, actorID, actorName, action, entryID, detail, ip, nowUTC())
}

// Count 审计总数（action 筛选与 List 一致），分页页码计算用
func Count(db *sql.DB, action string) (int, error) {
	q := `SELECT COUNT(*) FROM audit_logs`
	args := []any{}
	if action != "" {
		q += ` WHERE action = ?`
		args = append(args, action)
	}
	var n int
	err := db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// List 查询审计（action / actor 筛选 + 分页）
func List(db *sql.DB, action string, limit, offset int) ([]model.AuditLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, actor_type, actor_id, actor_name, action, entry_id, detail, ip, created_at
		FROM audit_logs`
	args := []any{}
	if action != "" {
		q += ` WHERE action = ?`
		args = append(args, action)
	}
	q += ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.AuditLog
	for rows.Next() {
		var l model.AuditLog
		var entryID sql.NullInt64
		if err := rows.Scan(&l.ID, &l.ActorType, &l.ActorID, &l.ActorName, &l.Action, &entryID, &l.Detail, &l.IP, &l.CreatedAt); err != nil {
			return nil, err
		}
		if entryID.Valid {
			e := entryID.Int64
			l.EntryID = &e
		}
		list = append(list, l)
	}
	return list, rows.Err()
}
