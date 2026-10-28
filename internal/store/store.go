package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lpx0312/keyHive/internal/crypto"
	"github.com/lpx0312/keyHive/internal/model"
)

// Store 条目/模板存取：负责 fields JSON 与敏感字段加解密的衔接。
// rotate-key 会并发替换 Cipher，故以 RWMutex 保护；DataDir/KeyFilePath/KeyFromEnv
// 供密钥轮换时决定新密钥的持久化位置。
type Store struct {
	DB          *sql.DB
	Cipher      *crypto.Cipher
	DataDir     string
	KeyFilePath string
	KeyFromEnv  bool

	mu sync.RWMutex

	// writeMu 进程级写互斥（TOCTOU 兜底）：轮换是"预读旧密文 → 事务重写 → 换内存密钥"，
	// 与并发条目写交错时，预读之后、提交之前落库的旧钥密文会在换钥后永久不可解密
	// （连接池 MaxOpenConns(1) 只串行化语句，不串行化逻辑上的读-改-写）。
	// 条目全部写方法（Create/Update/Delete）与 RotateAllEntries 入口共用此锁；
	// 读方法不加锁（保持现状）。锁内不再调用彼此，无重入/死锁风险。
	writeMu sync.Mutex
}

// cipher 并发安全取当前密钥
func (s *Store) cipher() *crypto.Cipher {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Cipher
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// encryptFields 敏感字段值 → 密文后序列化
func (s *Store) encryptFields(fields []model.Field) (string, error) {
	return encryptFieldsWith(s.cipher(), fields)
}

// encryptFieldsWith 用指定密钥加密（rotate 预计算新密文时使用）。
// 写入路径拿到的一律是"待加密明文"：创建/导入为用户明文；更新前 *** 哨兵已还原为
// 旧值明文；轮换前旧密文已解密。故不做 enc:v1: 前缀跳过——否则用户明文恰好以该
// 前缀开头时会原样入库（库里混入未加密的"伪密文"），读取时强制解密必败。
func encryptFieldsWith(c *crypto.Cipher, fields []model.Field) (string, error) {
	out := make([]model.Field, len(fields))
	copy(out, fields)
	for i := range out {
		if out[i].IsSecret && out[i].Value != "" {
			enc, err := c.Encrypt(out[i].Value)
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
	c := s.cipher()
	var fields []model.Field
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, err
	}
	for i := range fields {
		if fields[i].IsSecret && fields[i].Value != "" {
			plain, err := c.Decrypt(fields[i].Value)
			if err != nil {
				return nil, fmt.Errorf("字段 %q 解密失败: %w", fields[i].Key, err)
			}
			fields[i].Value = plain
		}
	}
	return fields, nil
}

const entryCols = `id, title, category, description, fields, tags, ai_visible, created_at, updated_at`

func (s *Store) scanEntry(row interface{ Scan(...any) error }) (*model.Entry, error) {
	var e model.Entry
	var fieldsJSON, tagsJSON string
	var aiVisible int
	if err := row.Scan(&e.ID, &e.Title, &e.Category, &e.Description, &fieldsJSON, &tagsJSON, &aiVisible, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	e.AIVisible = aiVisible == 1
	var err error
	e.Fields, err = s.decryptFields(fieldsJSON)
	if err != nil {
		// 解密失败时仍带出已扫描出的条目（含 ID），列表路径据此记录坏行并跳过
		return &e, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if msg := e.Validate(); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	fieldsJSON, err := s.encryptFields(e.Fields)
	if err != nil {
		return err
	}
	tagsJSON, _ := json.Marshal(e.Tags)
	now := nowUTC()
	aiVis := 0
	if e.AIVisible {
		aiVis = 1
	}
	res, err := s.DB.Exec(
		`INSERT INTO entries (title, category, description, fields, tags, ai_visible, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Title, e.Category, e.Description, fieldsJSON, string(tagsJSON), aiVis, now, now)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	e.CreatedAt, e.UpdatedAt = now, now
	return nil
}

// UpdateEntry 更新；maskSentinel 值（"***"）表示保留原值不动
func (s *Store) UpdateEntry(id int64, e *model.Entry) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
	tagsJSON, _ := json.Marshal(e.Tags)
	aiVis := 0
	if e.AIVisible {
		aiVis = 1
	}
	_, err = s.DB.Exec(
		`UPDATE entries SET title=?, category=?, description=?, fields=?, tags=?, ai_visible=?, updated_at=? WHERE id=?`,
		e.Title, e.Category, e.Description, fieldsJSON, string(tagsJSON), aiVis, nowUTC(), id)
	return err
}

// GetEntry 读取（敏感字段已解密为明文，注意输出前遮蔽）
func (s *Store) GetEntry(id int64) (*model.Entry, error) {
	return s.scanEntry(s.DB.QueryRow(`SELECT `+entryCols+` FROM entries WHERE id = ?`, id))
}

// DeleteEntry
func (s *Store) DeleteEntry(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	_, err := s.DB.Exec(`DELETE FROM entries WHERE id = ?`, id)
	return err
}

// ListEntries 列表（title 模糊 + category 精确筛选），敏感值已解密，调用方决定遮蔽
func (s *Store) ListEntries(q, category string) ([]model.Entry, error) {
	sb := strings.Builder{}
	sb.WriteString(`SELECT ` + entryCols + ` FROM entries WHERE 1=1`)
	args := []any{}
	if q != "" {
		// fields 是 JSON 文本：非敏感值（含 URL/用户名等）明文可 LIKE，敏感值是密文天然搜不到
		sb.WriteString(` AND (title LIKE ? OR description LIKE ? OR category LIKE ? OR fields LIKE ? OR tags LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like, like, like)
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
	// 初始化为空切片：nil slice 会被 json.Marshal 成 null，前端 entries.value=null 渲染崩溃
	list := make([]model.Entry, 0)
	bad := 0
	var firstErr error
	for rows.Next() {
		e, err := s.scanEntry(rows)
		if err != nil {
			// 单条坏行（JSON 损坏/密文解密失败）降级：记录条目 ID 后跳过，不拖垮整个列表
			// （导出、AI 检索等所有列表路径均经此函数，一并获得容错；GetEntry 仍保持严格报错）
			bad++
			if firstErr == nil {
				firstErr = err
			}
			var id int64
			if e != nil {
				id = e.ID
			}
			log.Printf("store: 条目 %d 读取失败，列表中跳过: %v", id, err)
			continue
		}
		list = append(list, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 命中的行全部损坏：更像主密钥不匹配或整库损坏等系统性问题，此时仍整体报错，
	// 避免把"全军覆没"静默降级成空列表
	if bad > 0 && len(list) == 0 {
		return nil, fmt.Errorf("列表命中 %d 条均读取失败（疑似主密钥不匹配或数据损坏）: %w", bad, firstErr)
	}
	return list, nil
}

// Categories 全部已用分类标签
func (s *Store) Categories() ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT category FROM entries ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]string, 0) // 同上：空结果必须是 [] 而非 null
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
	list := make([]model.Template, 0) // 同上：空结果必须是 [] 而非 null
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
		// 内置模板允许编辑（改名/字段；种子为 INSERT OR IGNORE 且仅首启执行，改动不会被升级覆盖），删除仍限自建
		res, err := s.DB.Exec(`UPDATE category_templates SET name=?, category=?, group_name=?, fields=?, updated_at=? WHERE id=?`,
			t.Name, t.Category, t.Group, string(fieldsJSON), now, t.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("模板不存在")
		}
		return nil
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

// RotateAllEntries 用新密钥重加密全部条目与 settings 表中同 Cipher 加密的密文
// （如 ai_llm_api_key），并更新 settings.key_check；
// 事务保证原子（失败回滚，旧密钥仍有效），成功后才替换内存密钥。返回重加密条数。
// 与条目写方法共用 writeMu：预读→提交窗口内不允许并发写入旧钥密文
// （本函数只走直接 SQL，不调用 Create/Update/DeleteEntry，加锁无重入）。
func (s *Store) RotateAllEntries(newCipher *crypto.Cipher) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// 1) 读取全部密文（旧密钥仍生效）
	rows, err := s.DB.Query(`SELECT id, fields FROM entries`)
	if err != nil {
		return 0, err
	}
	type rec struct {
		id  int64
		raw string
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.raw); err != nil {
			rows.Close()
			return 0, err
		}
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// 2) 预计算：旧密钥解密 → 新密钥加密
	type upd struct {
		id     int64
		fields string
	}
	updates := make([]upd, 0, len(recs))
	for _, r := range recs {
		plain, err := s.decryptFields(r.raw)
		if err != nil {
			return 0, fmt.Errorf("条目 %d: %w", r.id, err)
		}
		newJSON, err := encryptFieldsWith(newCipher, plain)
		if err != nil {
			return 0, fmt.Errorf("条目 %d 重加密失败: %w", r.id, err)
		}
		updates = append(updates, upd{r.id, newJSON})
	}
	// 2.5) settings 表中用同一 Cipher 加密的值（如 ai_llm_api_key）同样需要换钥重加密；
	// key_check 除外——下方统一用新钥重新生成。仍处旧钥有效窗口，先解密再预计算新密文
	type settingUpd struct {
		key string
		val string
	}
	srows, err := s.DB.Query(`SELECT key, value FROM settings WHERE value LIKE 'enc:v1:%' AND key != 'key_check'`)
	if err != nil {
		return 0, err
	}
	oldCipher := s.cipher()
	settingUpdates := make([]settingUpd, 0)
	for srows.Next() {
		var k, v string
		if err := srows.Scan(&k, &v); err != nil {
			srows.Close()
			return 0, err
		}
		plain, err := oldCipher.Decrypt(v)
		if err != nil {
			srows.Close()
			return 0, fmt.Errorf("settings %q 解密失败: %w", k, err)
		}
		enc, err := newCipher.Encrypt(plain)
		if err != nil {
			srows.Close()
			return 0, fmt.Errorf("settings %q 重加密失败: %w", k, err)
		}
		settingUpdates = append(settingUpdates, settingUpd{k, enc})
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return 0, err
	}

	kc, err := newCipher.KeyCheck()
	if err != nil {
		return 0, err
	}

	// 3) 事务落库（fields 全量重写 + settings 密文换钥 + key_check 更新）
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	for _, u := range updates {
		if _, err := tx.Exec(`UPDATE entries SET fields = ? WHERE id = ?`, u.fields, u.id); err != nil {
			tx.Rollback()
			return 0, err
		}
	}
	// settings 密文与条目同事务换钥，保持原子性
	for _, u := range settingUpdates {
		if _, err := tx.Exec(`UPDATE settings SET value = ? WHERE key = ?`, u.val, u.key); err != nil {
			tx.Rollback()
			return 0, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES ('key_check', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, kc); err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	// 4) 成功后替换内存密钥（后续读写走新密钥）
	s.mu.Lock()
	s.Cipher = newCipher
	s.mu.Unlock()
	return len(updates), nil
}
