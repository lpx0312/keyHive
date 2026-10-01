package api

import (
	"net/http"
	"strings"

	"keyhive/internal/aichat"
	"keyhive/internal/audit"
	"keyhive/internal/auth"
	"keyhive/internal/model"
)

// settings 键
const (
	settingLLMBaseURL = "ai_llm_base_url"
	settingLLMAPIKey  = "ai_llm_api_key" // 密文存储
	settingLLMModel   = "ai_llm_model"
)

func (s *Server) getSetting(key string) string {
	var v string
	s.Store.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *Server) putSetting(key, value string) error {
	_, err := s.Store.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// maskedKey API Key 遮蔽显示：只露尾 4 位
func maskedKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "****"
	}
	return "****" + k[len(k)-4:]
}

// handleAIConfig GET 返回配置（key 遮蔽）；PUT 保存（key 原文加密落库；key 留空表示不变）
func (s *Server) handleAIConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		keyEnc := s.getSetting(settingLLMAPIKey)
		keyPlain := ""
		if keyEnc != "" {
			if dec, err := s.Store.Cipher.Decrypt(keyEnc); err == nil {
				keyPlain = dec
			}
		}
		writeJSON(w, 200, map[string]string{
			"base_url":    orDefault(s.getSetting(settingLLMBaseURL), aichat.DefaultBaseURL),
			"api_key":     maskedKey(keyPlain),
			"model":       orDefault(s.getSetting(settingLLMModel), aichat.DefaultModel),
			"configured":  boolStr(keyPlain != ""),
		})
		return
	}
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Model   string `json:"model"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if strings.TrimSpace(req.BaseURL) != "" {
		if err := s.putSetting(settingLLMBaseURL, strings.TrimSpace(req.BaseURL)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if strings.TrimSpace(req.Model) != "" {
		if err := s.putSetting(settingLLMModel, strings.TrimSpace(req.Model)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	// key 以 **** 开头（遮蔽回显值）视为未修改；空串视为清除
	if strings.TrimSpace(req.APIKey) != "" && !strings.HasPrefix(strings.TrimSpace(req.APIKey), "****") {
		enc, err := s.Store.Cipher.Encrypt(strings.TrimSpace(req.APIKey))
		if err != nil {
			writeErr(w, 500, "加密失败")
			return
		}
		if err := s.putSetting(settingLLMAPIKey, enc); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		uid, _ := auth.UserID(r.Context())
		audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionAIChatConfig, nil, `{}`, clientIP(r))
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleAIChat 对话式录入：LLM 解析自然语言为条目草稿（草稿入库由前端确认后走 POST /entries）
func (s *Server) handleAIChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []aichat.ChatMessage `json:"messages"` // 之前的对话轮次（不含本次）
		Text     string               `json:"text"`     // 本次用户输入
	}
	if err := readBody(r, &req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeErr(w, 400, "请求格式错误：需要 text 字段")
		return
	}
	if len(req.Messages) > 40 {
		req.Messages = req.Messages[len(req.Messages)-40:]
	}

	keyEnc := s.getSetting(settingLLMAPIKey)
	if keyEnc == "" {
		writeErr(w, 400, "AI 未配置：请先在「设置 → AI 配置」填写 API Key")
		return
	}
	apiKey, err := s.Store.Cipher.Decrypt(keyEnc)
	if err != nil {
		writeErr(w, 500, "API Key 解密失败（主密钥变更？）")
		return
	}
	cfg := aichat.Config{
		BaseURL: orDefault(s.getSetting(settingLLMBaseURL), aichat.DefaultBaseURL),
		APIKey:  apiKey,
		Model:   orDefault(s.getSetting(settingLLMModel), aichat.DefaultModel),
	}

	// category 清单来自模板表
	categories := []string{}
	tpls, err := s.Store.ListTemplates()
	if err == nil {
		seen := map[string]bool{}
		for _, t := range tpls {
			if t.Category != "blank" && !seen[t.Category] {
				seen[t.Category] = true
				categories = append(categories, t.Category)
			}
		}
	}

	resp, err := aichat.Chat(r.Context(), cfg, aichat.SystemPrompt(categories), req.Messages, req.Text)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	if resp.Draft != nil {
		audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionAIChatDraft, nil,
			`{"title":"`+resp.Draft.Title+`","category":"`+resp.Draft.Category+`"}`, clientIP(r))
	}
	writeJSON(w, 200, resp)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// handleAIConfigTest 测试 LLM 连通性：优先用表单当前值；Key 为空/遮蔽时回退已保存值
func (s *Server) handleAIConfigTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Model   string `json:"model"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" || strings.HasPrefix(apiKey, "****") {
		keyEnc := s.getSetting(settingLLMAPIKey)
		if keyEnc == "" {
			writeJSON(w, 200, map[string]any{"ok": false,
				"error": "未填写 API Key，且系统里也没有已保存的 Key"})
			return
		}
		dec, err := s.Store.Cipher.Decrypt(keyEnc)
		if err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "error": "已保存 Key 解密失败"})
			return
		}
		apiKey = dec
	}
	cfg := aichat.Config{
		BaseURL: orDefault(strings.TrimSpace(req.BaseURL), orDefault(s.getSetting(settingLLMBaseURL), aichat.DefaultBaseURL)),
		APIKey:  apiKey,
		Model:   orDefault(strings.TrimSpace(req.Model), orDefault(s.getSetting(settingLLMModel), aichat.DefaultModel)),
	}
	writeJSON(w, 200, aichat.TestConnection(r.Context(), cfg))
}
