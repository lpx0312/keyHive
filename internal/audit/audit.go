package audit

import (
	"database/sql"
	"github.com/lpx0312/keyHive/internal/model"
	"time"
)

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
