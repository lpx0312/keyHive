package model

import "strings"

// 字段类型
const (
 FieldTypeText     = "text"
 FieldTypeURL      = "url"
 FieldTypeMultiline = "multiline"
)

// Field 条目自定义字段：数量任意，Description 为给 AI 看的说明
type Field struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Type        string `json:"type"`
	IsSecret    bool   `json:"is_secret"`
	// 存储层：IsSecret 时为密文（enc:v1:base64）；服务层解密后为明文；API 输出时遮蔽
	Value string `json:"value,omitempty"`
}

// MaskedValue 遮蔽占位
const MaskedValue = "***"

func (f Field) ValidType() bool {
	switch f.Type {
	case FieldTypeText, FieldTypeURL, FieldTypeMultiline, "":
		return true
	}
	return false
}

// Normalize 规范化字段名：去首尾空白
func (f *Field) Normalize() {
	f.Key = strings.TrimSpace(f.Key)
	f.Description = strings.TrimSpace(f.Description)
	if f.Type == "" {
		f.Type = FieldTypeText
	}
}

// Entry 一条密钥条目
type Entry struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	AIVisible   bool    `json:"ai_visible"`
	Fields      []Field `json:"fields"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// FieldByKey 按字段名查找
func (e *Entry) FieldByKey(key string) *Field {
	for i := range e.Fields {
		if e.Fields[i].Key == key {
			return &e.Fields[i]
		}
	}
	return nil
}

// MaskSecrets 将敏感字段值替换为遮蔽占位（原地）
func (e *Entry) MaskSecrets() {
	for i := range e.Fields {
		if e.Fields[i].IsSecret {
			e.Fields[i].Value = MaskedValue
		}
	}
}

// Validate 创建/更新时的校验
func (e *Entry) Validate() string {
	e.Title = strings.TrimSpace(e.Title)
	e.Category = strings.TrimSpace(e.Category)
	e.Description = strings.TrimSpace(e.Description)
	if e.Title == "" {
		return "标题不能为空"
	}
	if e.Category == "" {
		e.Category = "misc"
	}
	seen := map[string]bool{}
	for i := range e.Fields {
		f := &e.Fields[i]
		f.Normalize()
		if f.Key == "" {
			return "存在未填写名称的字段"
		}
		if seen[f.Key] {
			return "字段名重复：" + f.Key
		}
		seen[f.Key] = true
		if !f.ValidType() {
			return "字段类型无效：" + f.Key
		}
	}
	return ""
}

// Template 录入模板（内置或用户自建）
type Template struct {
	ID        int64   `json:"id"`
	Category  string  `json:"category"` // 模板标识，如 docker_registry
	Name      string  `json:"name"`
	Group     string  `json:"group"` // 分组：云平台/容器镜像/...
	Fields    []Field `json:"fields"`
	Builtin   bool    `json:"builtin"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// APIToken AI 访问令牌（库中只存哈希）
type APIToken struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at"`
	ExpiresAt  *string  `json:"expires_at"`
	RevokedAt  *string  `json:"revoked_at"`
	// Token 仅创建响应中携带明文，其余时候为空
	Token string `json:"token,omitempty"`
}

func (t *APIToken) HasScope(s string) bool {
	for _, v := range t.Scopes {
		if v == s {
			return true
		}
	}
	return false
}

func (t *APIToken) Effective() bool {
	return t.RevokedAt == nil
}

// AuditLog 审计日志
type AuditLog struct {
	ID        int64  `json:"id"`
	ActorType string `json:"actor_type"` // user | token | system
	ActorID   int64  `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Action    string `json:"action"`
	EntryID   *int64 `json:"entry_id"`
	Detail    string `json:"detail"`
	IP        string `json:"ip"`
	CreatedAt string `json:"created_at"`
}

// 审计动作
const (
	ActionLogin        = "login"
	ActionLoginFailed  = "login_failed"
	ActionLogout       = "logout"
	ActionEntryCreate  = "entry_create"
	ActionEntryUpdate  = "entry_update"
	ActionEntryDelete  = "entry_delete"
	ActionEntryReveal  = "entry_reveal" // 人工查看敏感值
	ActionFieldReveal  = "field_reveal" // AI 取敏感值
	ActionTokenCreate  = "token_create"
	ActionTokenRevoke  = "token_revoke"
	ActionTemplateSave = "template_save"
	ActionTemplateDel  = "template_delete"
	ActionPassword     = "password_change"
	ActionAIChatDraft  = "ai_chat_draft"  // AI 助手生成条目草稿
	ActionAIChatConfig = "ai_chat_config" // AI 助手 LLM 配置变更
	ActionUserCreate   = "user_create"
	ActionUserUpdate   = "user_update"
	ActionUserDelete   = "user_delete"
	ActionExport       = "export"     // 全库明文导出（admin）
	ActionKeyRotate    = "key_rotate" // 主密钥轮换（admin）
)

// User 登录账号
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	IsAdmin   bool   `json:"is_admin"`
	Disabled  bool   `json:"disabled"`
	CreatedAt string `json:"created_at"`
}
