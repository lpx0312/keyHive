// Package cli 实现 keyhive 客户端子命令（list/search/get/reveal/status），
// 通过 REST API 访问 keyHive 服务；internal/mcpserver 复用同一批调用函数。
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config ~/.keyhive/config.json
type Config struct {
	BaseURL     string `json:"base_url"`
	TokenRead   string `json:"token_read"`
	TokenReveal string `json:"token_reveal"`
}

const defaultBaseURL = "http://localhost:8020"

// ConfigPath 配置文件位置（KEYHIVE_CONFIG 环境变量可覆盖）
func ConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("KEYHIVE_CONFIG")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".keyhive/config.json"
	}
	return filepath.Join(home, ".keyhive", "config.json")
}

// LoadConfig 读取配置；文件不存在时返回带默认值的空配置
func LoadConfig() (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("配置文件格式错误 %s: %w", ConfigPath(), err)
	}
	return cfg, nil
}

// ErrServiceDown 服务不可达（Run 层转成启动指引）
type ErrServiceDown struct{ Base string }

func (e *ErrServiceDown) Error() string { return "无法连接 " + e.Base }

func (e *ErrServiceDown) hint() string {
	return "服务未启动？docker start keyhive（或 cd keyHive 目录 docker compose up -d）"
}

// apiGet/apiPost 调 AI API；respBody 为原始返回（错误时也返回 body 供展示）
func apiCall(cfg *Config, token, method, path string, body []byte) ([]byte, int, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, base+"/api/v1/ai"+path, rd)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, &ErrServiceDown{Base: base}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

// ListEntries / SearchEntries / GetEntry / RevealField：可复用调用入口
func ListEntries(cfg *Config, category string) ([]byte, int, error) {
	path := "/entries"
	if category != "" {
		path += "?category=" + url.QueryEscape(category)
	}
	return apiCall(cfg, cfg.TokenRead, http.MethodGet, path, nil)
}

func SearchEntries(cfg *Config, q string) ([]byte, int, error) {
	return apiCall(cfg, cfg.TokenRead, http.MethodGet, "/search?q="+url.QueryEscape(q), nil)
}

func GetEntry(cfg *Config, id string) ([]byte, int, error) {
	return apiCall(cfg, cfg.TokenRead, http.MethodGet, "/entries/"+url.PathEscape(id), nil)
}

func RevealField(cfg *Config, id, field string) ([]byte, int, error) {
	body, _ := json.Marshal(map[string]string{"field": field})
	return apiCall(cfg, cfg.TokenReveal, http.MethodPost, "/entries/"+url.PathEscape(id)+"/reveal", body)
}

// tokenHint 令牌未配置时的指引
func tokenHint(kind string) string {
	return fmt.Sprintf("令牌未配置（%s 中 %s 为空）。请到 keyHive Web UI「AI 令牌」页创建令牌（勾选 read/search/reveal），填入 %s",
		ConfigPath(), kind, ConfigPath())
}

func printJSON(data []byte) {
	var buf bytes.Buffer
	if json.Indent(&buf, data, "", "  ") == nil {
		os.Stdout.Write(buf.Bytes())
	} else {
		os.Stdout.Write(data)
	}
	os.Stdout.Write([]byte("\n"))
}

// Run CLI 入口：args 为子命令及参数（不含程序名），返回进程退出码
func Run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "status":
		return cmdStatus(cfg)
	case "list":
		category := ""
		if len(rest) >= 2 && rest[0] == "--category" {
			category = rest[1]
		}
		return runRO(cfg, func() ([]byte, int, error) { return ListEntries(cfg, category) }, "token_read")
	case "search":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "用法: keyhive search <关键词>")
			return 2
		}
		return runRO(cfg, func() ([]byte, int, error) { return SearchEntries(cfg, rest[0]) }, "token_read")
	case "get":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "用法: keyhive get <id>")
			return 2
		}
		return runRO(cfg, func() ([]byte, int, error) { return GetEntry(cfg, rest[0]) }, "token_read")
	case "reveal":
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, "用法: keyhive reveal <id> <字段名>")
			return 2
		}
		if cfg.TokenReveal == "" {
			fmt.Fprintln(os.Stderr, "错误:", tokenHint("token_reveal"))
			return 1
		}
		data, code, err := RevealField(cfg, rest[0], rest[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误:", err)
			if sd, ok := err.(*ErrServiceDown); ok {
				fmt.Fprintln(os.Stderr, sd.hint())
			}
			return 1
		}
		if code != 200 {
			os.Stderr.Write(append(data, '\n'))
			return 1
		}
		printJSON(data)
		fmt.Fprintln(os.Stderr, "⚠️  已取明文并记录审计；不要写入文件/git/对话正文")
		return 0
	default:
		usage()
		return 2
	}
}

// runRO 只读命令的公共流程（token 检查 → 调用 → 错误处理 → 输出）
func runRO(cfg *Config, call func() ([]byte, int, error), tokenField string) int {
	if cfg.TokenRead == "" {
		fmt.Fprintln(os.Stderr, "错误:", tokenHint(tokenField))
		return 1
	}
	data, code, err := call()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		if sd, ok := err.(*ErrServiceDown); ok {
			fmt.Fprintln(os.Stderr, sd.hint())
		}
		return 1
	}
	if code != 200 {
		os.Stderr.Write(append(data, '\n'))
		return 1
	}
	printJSON(data)
	return 0
}

func cmdStatus(cfg *Config) int {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	fmt.Println("base_url    :", base)
	fmt.Println("config      :", ConfigPath())
	fmt.Println("token_read  :", pick(cfg.TokenRead != "", "已配置", "未配置"))
	fmt.Println("token_reveal:", pick(cfg.TokenReveal != "", "已配置", "未配置"))
	if cfg.TokenRead == "" {
		fmt.Println("下一步：Web UI (http://localhost:8020)「AI 令牌」页创建令牌（勾选 read/search/reveal），填入上述配置文件")
		return 1
	}
	data, code, err := ListEntries(cfg, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "连通性: 失败 -", err)
		if sd, ok := err.(*ErrServiceDown); ok {
			fmt.Fprintln(os.Stderr, sd.hint())
		}
		return 1
	}
	if code == 200 {
		var list []map[string]any
		if json.Unmarshal(data, &list) == nil {
			fmt.Printf("连通性: 正常（list 权限 OK，可见条目 %d 条）\n", len(list))
		}
		return 0
	}
	fmt.Fprintln(os.Stderr, "连通性: 异常 HTTP", code)
	os.Stderr.Write(append(data, '\n'))
	return 1
}

func pick(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

func usage() {
	fmt.Fprintln(os.Stderr, `keyhive 密钥管家客户端

用法:
  keyhive serve                     启动服务（默认）
  keyhive status                    检查服务/配置/令牌
  keyhive list [--category <分类>]  列出条目（遮蔽，含注释）
  keyhive search <关键词>           搜索
  keyhive get <id>                  条目详情（遮蔽）
  keyhive reveal <id> <字段名>      取单字段明文（记审计）
  keyhive mcp                       以 stdio MCP server 运行（供 AI 客户端接入）

配置: ~/.keyhive/config.json（KEYHIVE_CONFIG 环境变量可覆盖）`)
}
