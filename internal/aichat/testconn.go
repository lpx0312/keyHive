package aichat

import (
	"context"
	"strings"
)

// TestConnection 测试 LLM 配置连通性（发一条最小消息，不带工具）
func TestConnection(ctx context.Context, cfg Config) map[string]any {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	resp, err := Chat(ctx, cfg, "你是连通性测试助手。", nil, "ping", ToolCallbacks{})
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return map[string]any{"ok": true, "model": cfg.Model, "reply": truncate(strings.TrimSpace(resp.Reply), 60)}
}
