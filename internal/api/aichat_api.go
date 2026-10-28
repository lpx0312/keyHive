package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lpx0312/keyHive/internal/aichat"
	"github.com/lpx0312/keyHive/internal/audit"
	"github.com/lpx0312/keyHive/internal/auth"
	"github.com/lpx0312/keyHive/internal/model"
)

// settings 键
const (
	settingLLMProtocol = "ai_llm_protocol" // openai（默认）| anthropic
	settingLLMBaseURL  = "ai_llm_base_url"
	settingLLMAPIKey   = "ai_llm_api_key" // 密文存储
	settingLLMModel    = "ai_llm_model"
)

// currentLLMConfig 组装当前生效的 LLM 配置（settings 缺省时按协议默认值兜底）。
// BaseURL 不做任何预填：空值原样传给 aichat 层，由其按协议分派默认端点
// （openai → 智谱 OpenAI 兼容端点；anthropic → Anthropic 官方端点）。
// 这里若协议盲地预填 aichat.DefaultBaseURL（OpenAI 端点），anthropic + 空 baseURL
// 时 x-api-key 会被发往 open.bigmodel.cn，aichat 层的按协议分派在生产路径成为死代码。
func (s *Server) currentLLMConfig() aichat.Config {
	return aichat.Config{
		Protocol: orDefault(s.getSetting(settingLLMProtocol), aichat.ProtocolOpenAI),
		BaseURL:  strings.TrimSpace(s.getSetting(settingLLMBaseURL)),
		Model:    orDefault(s.getSetting(settingLLMModel), aichat.DefaultModel),
	}
}

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
		// base_url 存什么回什么：未设置时不下发该字段（空就空，前端表单有 placeholder），
		// 不回填伪造的默认端点——否则前端把回显值原样保存，协议切换后的默认端点分派被固化失效
		resp := map[string]any{
			"protocol":   orDefault(s.getSetting(settingLLMProtocol), aichat.ProtocolOpenAI),
			"api_key":    maskedKey(keyPlain),
			"model":      orDefault(s.getSetting(settingLLMModel), aichat.DefaultModel),
			"configured": boolStr(keyPlain != ""),
			"presets":    aichat.Presets,
		}
		if baseURL := strings.TrimSpace(s.getSetting(settingLLMBaseURL)); baseURL != "" {
			resp["base_url"] = baseURL
		}
		writeJSON(w, 200, resp)
		return
	}
	var req struct {
		Protocol string `json:"protocol"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
	}
	if err := readBody(r, &req); err != nil {
		writeReadErr(w, err)
		return
	}
	if p := strings.TrimSpace(req.Protocol); p != "" {
		if p != aichat.ProtocolOpenAI && p != aichat.ProtocolAnthropic {
			writeErr(w, 400, "protocol 仅支持 openai / anthropic")
			return
		}
		if err := s.putSetting(settingLLMProtocol, p); err != nil {
			writeInternalErr(w, err)
			return
		}
	}
	if strings.TrimSpace(req.BaseURL) != "" {
		if err := s.putSetting(settingLLMBaseURL, strings.TrimSpace(req.BaseURL)); err != nil {
			writeInternalErr(w, err)
			return
		}
	}
	if strings.TrimSpace(req.Model) != "" {
		if err := s.putSetting(settingLLMModel, strings.TrimSpace(req.Model)); err != nil {
			writeInternalErr(w, err)
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
			writeInternalErr(w, err)
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
	if err := readBody(r, &req); err != nil {
		writeReadErr(w, err)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
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
	cfg := s.currentLLMConfig()
	cfg.APIKey = apiKey

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

	// 查询回调：复用列表逻辑，只给 LLM ai_visible=true 的条目且敏感值遮蔽（隐身条目内容不出库）
	cb := aichat.ToolCallbacks{
		SearchEntries: func(query string) ([]model.Entry, error) {
			list, err := s.Store.ListEntries(query, "")
			if err != nil {
				return nil, err
			}
			out := make([]model.Entry, 0, len(list))
			for i := range list {
				if !list[i].AIVisible {
					continue
				}
				list[i].MaskSecrets()
				out = append(out, list[i])
			}
			return out, nil
		},
	}

	resp, err := aichat.Chat(r.Context(), cfg, aichat.SystemPrompt(categories), req.Messages, req.Text, cb)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	uid, _ := auth.UserID(r.Context())
	if resp.Draft != nil {
		detail := map[string]any{"kind": resp.DraftKind, "title": resp.Draft.Title, "category": resp.Draft.Category}
		if resp.DraftEntryID > 0 {
			detail["entry_id"] = resp.DraftEntryID
		}
		audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionAIChatDraft, nil,
			audit.Detail(detail), clientIP(r))
	}
	writeJSON(w, 200, resp)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// handleAIChatStream 流式对话：SSE 推送 status/reply_delta/draft/done 事件
func (s *Server) handleAIChatStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []aichat.ChatMessage `json:"messages"`
		Text     string               `json:"text"`
	}
	if err := readBody(r, &req); err != nil {
		writeReadErr(w, err)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
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
	cfg := s.currentLLMConfig()
	cfg.APIKey = apiKey

	categories := []string{}
	if tpls, err := s.Store.ListTemplates(); err == nil {
		seen := map[string]bool{}
		for _, t := range tpls {
			if t.Category != "blank" && !seen[t.Category] {
				seen[t.Category] = true
				categories = append(categories, t.Category)
			}
		}
	}

	cb := aichat.ToolCallbacks{
		SearchEntries: func(query string) ([]model.Entry, error) {
			list, err := s.Store.ListEntries(query, "")
			if err != nil {
				return nil, err
			}
			out := make([]model.Entry, 0, len(list))
			for i := range list {
				if !list[i].AIVisible {
					continue
				}
				list[i].MaskSecrets()
				out = append(out, list[i])
			}
			return out, nil
		},
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "当前连接不支持流式输出")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	sendEvent := func(ev aichat.StreamEvent) error {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return nil
	}

	resp, err := aichat.ChatStream(r.Context(), cfg, aichat.SystemPrompt(categories), req.Messages, req.Text, cb, sendEvent)
	if err != nil {
		sendEvent(aichat.StreamEvent{Type: "error", Text: err.Error()})
		return
	}
	// 草稿以独立事件补发（前端渲染确认卡片）；随后 done 收尾
	if resp.Draft != nil {
		sendEvent(aichat.StreamEvent{Type: "draft", Text: resp.Reply, Draft: resp.Draft,
			DraftKind: resp.DraftKind, DraftEntryID: resp.DraftEntryID})
		uid, _ := auth.UserID(r.Context())
		detail := map[string]any{"kind": resp.DraftKind, "title": resp.Draft.Title, "category": resp.Draft.Category}
		if resp.DraftEntryID > 0 {
			detail["entry_id"] = resp.DraftEntryID
		}
		audit.Log(s.Store.DB, "user", uid, s.usernameByID(uid), model.ActionAIChatDraft, nil,
			audit.Detail(detail), clientIP(r))
	}
	sendEvent(aichat.StreamEvent{Type: "done", Text: resp.Reply})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// handleAIConfigTest 测试 LLM 连通性（仅 admin）：优先用表单当前值；Key 为空/遮蔽时回退已保存值
func (s *Server) handleAIConfigTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Protocol string `json:"protocol"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
	}
	if err := readBody(r, &req); err != nil {
		writeReadErr(w, err)
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
	saved := s.currentLLMConfig()
	// 表单与已保存值均为空时 BaseURL 保持空串，由 aichat 层按协议分派默认端点
	cfg := aichat.Config{
		Protocol: orDefault(strings.TrimSpace(req.Protocol), saved.Protocol),
		BaseURL:  orDefault(strings.TrimSpace(req.BaseURL), saved.BaseURL),
		APIKey:   apiKey,
		Model:    orDefault(strings.TrimSpace(req.Model), saved.Model),
	}
	writeJSON(w, 200, aichat.TestConnection(r.Context(), cfg))
}

// handleAIListModels 拉取服务商模型清单（前端「获取模型列表」下拉用，仅 admin）：
// 优先用表单当前值，Key 为空/遮蔽时回退已保存值；本地端点（Ollama 等）无 Key 也可拉取
func (s *Server) handleAIListModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Protocol string `json:"protocol"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	if err := readBody(r, &req); err != nil {
		writeReadErr(w, err)
		return
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" || strings.HasPrefix(apiKey, "****") {
		if keyEnc := s.getSetting(settingLLMAPIKey); keyEnc != "" {
			if dec, err := s.Store.Cipher.Decrypt(keyEnc); err == nil {
				apiKey = dec
			}
		}
	}
	saved := s.currentLLMConfig()
	// 同上：两端都空时 BaseURL 为空串，aichat 层按协议分派默认端点
	cfg := aichat.Config{
		Protocol: orDefault(strings.TrimSpace(req.Protocol), saved.Protocol),
		BaseURL:  orDefault(strings.TrimSpace(req.BaseURL), saved.BaseURL),
		APIKey:   apiKey,
	}
	models, err := aichat.ListModels(r.Context(), cfg)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "models": models})
}
