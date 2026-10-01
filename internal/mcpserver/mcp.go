// Package mcpserver 将 keyHive 客户端能力以 stdio MCP server 暴露
// （keyhive mcp），供 ZCode / Claude / Cursor 等 MCP 客户端接入。
// 工具实现复用 internal/cli 的调用函数，配置同 ~/.keyhive/config.json。
package mcpserver

import (
	"context"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"keyhive/internal/cli"
	"keyhive/internal/version"
)

const discipline = "纪律：先用遮蔽信息（字段名+description 注释）判断，能不取明文就不取；明文只注入执行环境，绝不写入文件/git/对话正文；每次 reveal 均记审计。"

// Run 启动 stdio MCP server，返回进程退出码
func Run() int {
	s := server.NewMCPServer("keyhive", version.Version,
		server.WithToolCapabilities(false),
		server.WithInstructions("keyHive 密钥管家：先 kh_list/kh_search 看有什么（敏感值遮蔽、注释完整），确需明文再 kh_reveal。" + discipline),
	)

	s.AddTool(mcp.NewTool("kh_status",
		mcp.WithDescription("检查 keyHive 服务连通性、配置文件与令牌配置状态。取密钥失败时先用这个排查。"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cfg, err := cli.LoadConfig()
		if err != nil {
			return text(err.Error()), nil
		}
		return callAndWrap(func() ([]byte, int, error) { return cli.ListEntries(cfg, "") })
	})

	s.AddTool(mcp.NewTool("kh_list",
		mcp.WithDescription("列出 AI 可见的密钥条目。敏感值遮蔽为 ***，但每个字段含用户手写的 description 注释，说明了字段含义与用法。category 可选。"),
		mcp.WithString("category", mcp.Description("按分类过滤，可省略")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cfg, err := requireRead()
		if err != nil {
			return text(err.Error()), nil
		}
		cat := req.GetString("category", "")
		return callAndWrap(func() ([]byte, int, error) { return cli.ListEntries(cfg, cat) })
	})

	s.AddTool(mcp.NewTool("kh_search",
		mcp.WithDescription("按关键词搜索密钥条目（匹配标题/说明/分类），返回遮蔽结果与注释。任务需要任何凭据（docker login、数据库、AK/SK、API key 等）时先用它查库里有没有，而不是问用户要。"),
		mcp.WithString("query", mcp.Required(), mcp.Description("搜索关键词，中文/英文均可")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cfg, err := requireRead()
		if err != nil {
			return text(err.Error()), nil
		}
		q, err := req.RequireString("query")
		if err != nil {
			return text("缺少 query 参数"), nil
		}
		return callAndWrap(func() ([]byte, int, error) { return cli.SearchEntries(cfg, q) })
	})

	s.AddTool(mcp.NewTool("kh_get",
		mcp.WithDescription("按 id 取单条密钥条目详情（敏感值遮蔽，注释完整）。id 来自 kh_list/kh_search 结果。"),
		mcp.WithString("id", mcp.Required(), mcp.Description("条目 id")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cfg, err := requireRead()
		if err != nil {
			return text(err.Error()), nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return text("缺少 id 参数"), nil
		}
		return callAndWrap(func() ([]byte, int, error) { return cli.GetEntry(cfg, id) })
	})

	s.AddTool(mcp.NewTool("kh_reveal",
		mcp.WithDescription("取某条目单个敏感字段的明文（需 reveal 权限，每次调用记录审计）。只在确需明文执行任务（如 docker login）时使用；返回值不要写入文件/git/对话正文。"+discipline),
		mcp.WithString("id", mcp.Required(), mcp.Description("条目 id")),
		mcp.WithString("field", mcp.Required(), mcp.Description("字段名（条目 fields 里的 key）")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cfg, err := cli.LoadConfig()
		if err != nil {
			return text(err.Error()), nil
		}
		if cfg.TokenReveal == "" {
			return text("token_reveal 未配置。请到 keyHive Web UI「AI 令牌」页创建（勾选 reveal）并填入 " + cli.ConfigPath()), nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return text("缺少 id 参数"), nil
		}
		field, err := req.RequireString("field")
		if err != nil {
			return text("缺少 field 参数"), nil
		}
		return callAndWrap(func() ([]byte, int, error) { return cli.RevealField(cfg, id, field) })
	})

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "MCP server 错误:", err)
		return 1
	}
	return 0
}

func requireRead() (*cli.Config, error) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.TokenRead == "" {
		return nil, fmt.Errorf("token_read 未配置。请到 keyHive Web UI「AI 令牌」页创建令牌并填入 %s", cli.ConfigPath())
	}
	return cfg, nil
}

func callAndWrap(call func() ([]byte, int, error)) (*mcp.CallToolResult, error) {
	data, code, err := call()
	if err != nil {
		return text("请求失败: " + err.Error()), nil
	}
	if code != 200 {
		return text(fmt.Sprintf("HTTP %d: %s", code, string(data))), nil
	}
	return text(string(data)), nil
}

func text(s string) *mcp.CallToolResult {
	return mcp.NewToolResultText(s)
}
