package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"keyhive/internal/crypto"
	"keyhive/internal/model"
)

// Store 条目/模板存取：负责 fields JSON 与敏感字段加解密的衔接
type Store struct {
	DB     *sql.DB
	Cipher *crypto.Cipher
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// encryptFields 敏感字段值 → 密文后序列化
func (s *Store) encryptFields(fields []model.Field) (string, error) {
	out := make([]model.Field, len(fields))
	copy(out, fields)
	for i := range out {
		if out[i].IsSecret && out[i].Value != "" && !crypto.IsEncrypted(out[i].Value) {
			enc, err := s.Cipher.Encrypt(out[i].Value)
			if err != nil {
				return "", err
			}
			out[i].Value = enc
		}
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// decryptFields 反序列化并解密敏感字段值
func (s *Store) decryptFields(raw string) ([]model.Field, error) {
	var fields []model.Field
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, err
	}
	for i := range fields {
		if fields[i].IsSecret && fields[i].Value != "" {
			plain, err := s.Cipher.Decrypt(fields[i].Value)
			if err != nil {
				return nil, fmt.Errorf("字段 %q 解密失败: %w", fields[i].Key, err)
			}
			fields[i].Value = plain
		}
	}
	return fields, nil
}

const entryCols = `id, title, category, description, fields, ai_visible, created_at, updated_at`

func (s *Store) scanEntry(row interface{ Scan(...any) error }) (*model.Entry, error) {
	var e model.Entry
	var fieldsJSON string
	var aiVisible int
	if err := row.Scan(&e.ID, &e.Title, &e.Category, &e.Description, &fieldsJSON, &aiVisible, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	e.AIVisible = aiVisible == 1
	var err error
	e.Fields, err = s.decryptFields(fieldsJSON)
	return &e, err
}

// NewForTest 测试夹具：随机主密钥构造 Store
func NewForTest(db *sql.DB) (*Store, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	c, err := crypto.New(raw)
	if err != nil {
		return nil, err
	}
	return &Store{DB: db, Cipher: c}, nil
}

// CreateEntry 新建（校验 + 加密 + 落库），返回完整条目
func (s *Store) CreateEntry(e *model.Entry) error {
	if msg := e.Validate(); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	fieldsJSON, err := s.encryptFields(e.Fields)
	if err != nil {
		return err
	}
	now := nowUTC()
	aiVis := 0
	if e.AIVisible {
		aiVis = 1
	}
	res, err := s.DB.Exec(
		`INSERT INTO entries (title, category, description, fields, ai_visible, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Title, e.Category, e.Description, fieldsJSON, aiVis, now, now)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	e.CreatedAt, e.UpdatedAt = now, now
	return nil
}

// UpdateEntry 更新；maskSentinel 值（"***"）表示保留原值不动
func (s *Store) UpdateEntry(id int64, e *model.Entry) error {
	if msg := e.Validate(); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	old, err := s.GetEntry(id)
	if err != nil {
		return err
	}
	// 前端未重新输入的敏感字段以 *** 回传，保留库中原值
	for i := range e.Fields {
		if e.Fields[i].IsSecret && e.Fields[i].Value == model.MaskedValue {
			if of := old.FieldByKey(e.Fields[i].Key); of != nil {
				e.Fields[i].Value = of.Value
			}
		}
	}
	fieldsJSON, err := s.encryptFields(e.Fields)
	if err != nil {
		return err
	}
	aiVis := 0
	if e.AIVisible {
		aiVis = 1
	}
	_, err = s.DB.Exec(
		`UPDATE entries SET title=?, category=?, description=?, fields=?, ai_visible=?, updated_at=? WHERE id=?`,
		e.Title, e.Category, e.Description, fieldsJSON, aiVis, nowUTC(), id)
	return err
}

// GetEntry 读取（敏感字段已解密为明文，注意输出前遮蔽）
func (s *Store) GetEntry(id int64) (*model.Entry, error) {
	return s.scanEntry(s.DB.QueryRow(`SELECT `+entryCols+` FROM entries WHERE id = ?`, id))
}

// DeleteEntry
func (s *Store) DeleteEntry(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM entries WHERE id = ?`, id)
	return err
}

// ListEntries 列表（title 模糊 + category 精确筛选），敏感值已解密，调用方决定遮蔽
func (s *Store) ListEntries(q, category string) ([]model.Entry, error) {
	sb := strings.Builder{}
	sb.WriteString(`SELECT ` + entryCols + ` FROM entries WHERE 1=1`)
	args := []any{}
	if q != "" {
		sb.WriteString(` AND (title LIKE ? OR description LIKE ? OR category LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	if category != "" {
		sb.WriteString(` AND category = ?`)
		args = append(args, category)
	}
	sb.WriteString(` ORDER BY updated_at DESC`)
	rows, err := s.DB.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.Entry
	for rows.Next() {
		e, err := s.scanEntry(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *e)
	}
	return list, rows.Err()
}

// Categories 全部已用分类标签
func (s *Store) Categories() ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT category FROM entries ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// ---- 模板 ----

const tplCols = `id, category, name, group_name, fields, builtin, created_at, updated_at`

func scanTemplate(row interface{ Scan(...any) error }) (*model.Template, error) {
	var t model.Template
	var fieldsJSON string
	var builtin int
	if err := row.Scan(&t.ID, &t.Category, &t.Name, &t.Group, &fieldsJSON, &builtin, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Builtin = builtin == 1
	err := json.Unmarshal([]byte(fieldsJSON), &t.Fields)
	return &t, err
}

func (s *Store) ListTemplates() ([]model.Template, error) {
	rows, err := s.DB.Query(`SELECT ` + tplCols + ` FROM category_templates ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *t)
	}
	return list, rows.Err()
}

func (s *Store) GetTemplate(id int64) (*model.Template, error) {
	return scanTemplate(s.DB.QueryRow(`SELECT `+tplCols+` FROM category_templates WHERE id = ?`, id))
}

// SaveTemplate 用户自建/从条目另存
func (s *Store) SaveTemplate(t *model.Template) error {
	t.Name = strings.TrimSpace(t.Name)
	t.Category = strings.TrimSpace(t.Category)
	t.Group = strings.TrimSpace(t.Group)
	if t.Name == "" {
		return fmt.Errorf("模板名不能为空")
	}
	if t.Category == "" {
		t.Category = "custom_" + fmt.Sprintf("%d", time.Now().Unix())
	}
	if t.Group == "" {
		t.Group = "我的模板"
	}
	for i := range t.Fields {
		t.Fields[i].Normalize()
		t.Fields[i].Value = "" // 模板只存骨架，不存值
	}
	fieldsJSON, _ := json.Marshal(t.Fields)
	now := nowUTC()
	if t.ID > 0 {
		_, err := s.DB.Exec(`UPDATE category_templates SET name=?, category=?, group_name=?, fields=?, updated_at=? WHERE id=? AND builtin=0`,
			t.Name, t.Category, t.Group, string(fieldsJSON), now, t.ID)
		return err
	}
	res, err := s.DB.Exec(`INSERT INTO category_templates (category, name, group_name, fields, builtin, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?)`, t.Category, t.Name, t.Group, string(fieldsJSON), now, now)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	t.CreatedAt, t.UpdatedAt = now, now
	return nil
}

func (s *Store) DeleteTemplate(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM category_templates WHERE id = ? AND builtin = 0`, id)
	return err
}
