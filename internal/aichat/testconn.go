package aichat

import (
	"context"
	"strings"
	"time"
)

// TestConnection 测试 LLM 配置连通性（发一条最小消息，带工具 schema，
// 实测往返耗时），返回 ok/model/protocol/latency_ms/reply
func TestConnection(ctx context.Context, cfg Config) map[string]any {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	start := time.Now()
	resp, err := Chat(ctx, cfg, "你是连通性测试助手。", nil, "ping", ToolCallbacks{})
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "latency_ms": ms}
	}
	return map[string]any{
		"ok":         true,
		"model":      cfg.Model,
		"protocol":   cfg.ProtocolOrDefault(),
		"latency_ms": ms,
		"reply":      truncate(strings.TrimSpace(resp.Reply), 60),
	}
}
