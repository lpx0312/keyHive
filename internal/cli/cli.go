// Package cli 实现 keyhive 客户端子命令（list/search/get/reveal/status），
// 通过 REST API 访问 keyHive 服务；internal/mcpserver 复用同一批调用函数。
package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"keyhive/internal/totp"
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

// AddEntry 人用通道录入：admin 登录换取会话 cookie 后创建条目（AI 令牌保持只读）
func AddEntry(cfg *Config, user, pass string, entryJSON []byte) ([]byte, int, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	loginResp, err := client.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		return nil, 0, &ErrServiceDown{Base: base}
	}
	lb, _ := io.ReadAll(loginResp.Body)
	loginResp.Body.Close()
	if loginResp.StatusCode != 200 {
		return lb, loginResp.StatusCode, fmt.Errorf("登录失败（HTTP %d）", loginResp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/entries", bytes.NewReader(entryJSON))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, &ErrServiceDown{Base: base}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
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
		category, staleDays := parseListArgs(rest)
		return cmdList(cfg, staleDays, func() ([]byte, int, error) { return ListEntries(cfg, category) })
	case "search":
		q, staleDays := parseSearchArgs(rest)
		if q == "" {
			fmt.Fprintln(os.Stderr, "用法: keyhive search <关键词> [--stale <天>]")
			return 2
		}
		return cmdList(cfg, staleDays, func() ([]byte, int, error) { return SearchEntries(cfg, q) })
	case "get":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "用法: keyhive get <id>")
			return 2
		}
		return runRO(cfg, func() ([]byte, int, error) { return GetEntry(cfg, rest[0]) }, "token_read")
	case "totp":
		return cmdTOTP(cfg, rest)
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
	case "add":
		return cmdAdd(cfg, rest)
	case "export":
		return cmdExport(cfg, rest)
	case "import":
		return cmdImport(cfg, rest)
	case "rotate-key":
		return cmdRotateKey(cfg, rest)
	default:
		usage()
		return 2
	}
}

// parseListArgs 解析 list 的 [--category c] [--stale N]
func parseListArgs(args []string) (category string, staleDays int) {
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--category":
			category = args[i+1]
		case "--stale":
			staleDays = atoi(args[i+1])
		}
	}
	return
}

// parseSearchArgs 解析 search 的 <关键词> [--stale N]（关键词位置不限）
func parseSearchArgs(args []string) (q string, staleDays int) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--stale" && i+1 < len(args) {
			staleDays = atoi(args[i+1])
			i++
		} else if q == "" && !strings.HasPrefix(args[i], "--") {
			q = args[i]
		}
	}
	return
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// cmdList list/search 公共流程：拉取 → 统计/过滤超期 → 输出
func cmdList(cfg *Config, staleDays int, call func() ([]byte, int, error)) int {
	rc, out := runROCapture(cfg, call, "token_read")
	if rc != 0 {
		return rc
	}
	stale := countStale(out, 90)
	if stale > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  %d 条超 90 天未更新（--stale 90 查看）\n", stale)
	}
	if staleDays > 0 {
		filtered, err := filterStale(out, staleDays)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误:", err)
			return 1
		}
		printJSON(filtered)
		return 0
	}
	printJSON(out)
	return 0
}

// runROCapture 与 runRO 相同流程但返回响应体（供后处理）
func runROCapture(cfg *Config, call func() ([]byte, int, error), tokenField string) (int, []byte) {
	if cfg.TokenRead == "" {
		fmt.Fprintln(os.Stderr, "错误:", tokenHint(tokenField))
		return 1, nil
	}
	data, code, err := call()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		if sd, ok := err.(*ErrServiceDown); ok {
			fmt.Fprintln(os.Stderr, sd.hint())
		}
		return 1, nil
	}
	if code != 200 {
		os.Stderr.Write(append(data, '\n'))
		return 1, nil
	}
	return 0, data
}

type staleEntry struct {
	UpdatedAt string `json:"updated_at"`
}

// countStale 统计超 N 天未更新的条目数
func countStale(data []byte, days int) int {
	var list []staleEntry
	if json.Unmarshal(data, &list) != nil {
		return 0
	}
	n := 0
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, e := range list {
		if t, err := time.Parse(time.RFC3339, e.UpdatedAt); err == nil && t.Before(cutoff) {
			n++
		}
	}
	return n
}

// filterStale 仅保留超 N 天未更新的条目（返回原始 JSON 值列表，保持字段不动）
func filterStale(data []byte, days int) ([]byte, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	out := make([]json.RawMessage, 0, len(raw))
	for _, item := range raw {
		var e staleEntry
		if json.Unmarshal(item, &e) == nil {
			if t, err := time.Parse(time.RFC3339, e.UpdatedAt); err == nil && t.Before(cutoff) {
				out = append(out, item)
			}
		}
	}
	return json.Marshal(out)
}

// cmdTOTP 生成条目的 6 位动态码（字段定位 → reveal 密钥 → 本地计算）
func cmdTOTP(cfg *Config, args []string) int {
	id, field := "", ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--field" && i+1 < len(args) {
			field = args[i+1]
			i++
		} else if id == "" {
			id = args[i]
		}
	}
	if id == "" {
		fmt.Fprintln(os.Stderr, "用法: keyhive totp <id> [--field totp_secret]")
		return 2
	}
	if cfg.TokenRead == "" || cfg.TokenReveal == "" {
		fmt.Fprintln(os.Stderr, "错误:", tokenHint("token_read/token_reveal"))
		return 1
	}
	// 1) 遮蔽版详情 → 定位 TOTP 字段
	rc, data := runROCapture(cfg, func() ([]byte, int, error) { return GetEntry(cfg, id) }, "token_read")
	if rc != 0 {
		return rc
	}
	var e struct {
		Title  string `json:"entry_title"`
		Fields []struct {
			Key         string `json:"key"`
			Description string `json:"description"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		fmt.Fprintln(os.Stderr, "错误: 响应解析失败:", err)
		return 1
	}
	if field == "" {
		for _, f := range e.Fields {
			if strings.Contains(strings.ToLower(f.Key), "totp") ||
				strings.Contains(f.Description, "TOTP") || strings.Contains(f.Description, "两步验证") {
				field = f.Key
				break
			}
		}
	}
	if field == "" {
		fmt.Fprintf(os.Stderr, "错误: 未在该条目中找到 TOTP 字段（key 含 totp 或说明含 TOTP/两步验证），请用 --field 指定\n")
		return 1
	}
	// 2) reveal 密钥明文（记审计）
	revData, code, err := RevealField(cfg, id, field)
	if err != nil || code != 200 {
		fmt.Fprintln(os.Stderr, "错误: 取密钥失败:", err)
		if code != 200 && revData != nil {
			os.Stderr.Write(append(revData, '\n'))
		}
		return 1
	}
	var rev struct {
		EntryTitle string `json:"entry_title"`
		Value      string `json:"value"`
	}
	json.Unmarshal(revData, &rev)
	// 3) 本地计算（兼容纯密钥与 otpauth:// URI）
	otp, remain, err := totp.Current(totp.ParseOTAuth(rev.Value))
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误: 字段值不是有效的 TOTP 密钥:", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "%s\n", otp)
	fmt.Fprintf(os.Stderr, "条目 %s | 字段 %s | %d 秒后过期 | 已记审计\n", orDefault(rev.EntryTitle, e.Title), field, remain)
	return 0
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
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

func cmdAdd(cfg *Config, args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	user := fs.String("user", "admin", "管理员用户名")
	pass := fs.String("pass", "", "管理员密码（推荐改用环境变量 KEYHIVE_ADMIN_PASS，避免进 shell 历史）")
	jsonFile := fs.String("file", "", "条目 JSON 文件路径；留空则读 stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pass == "" {
		*pass = strings.TrimSpace(os.Getenv("KEYHIVE_ADMIN_PASS"))
	}
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "错误: 未提供管理员密码（--pass 或环境变量 KEYHIVE_ADMIN_PASS）")
		return 1
	}
	var entryJSON []byte
	var err error
	if *jsonFile != "" {
		entryJSON, err = os.ReadFile(*jsonFile)
	} else {
		entryJSON, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误: 读取条目 JSON 失败:", err)
		return 1
	}
	if !json.Valid(entryJSON) {
		fmt.Fprintln(os.Stderr, "错误: 条目 JSON 格式无效")
		return 2
	}
	data, code, err := AddEntry(cfg, *user, *pass, entryJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		if sd, ok := err.(*ErrServiceDown); ok {
			fmt.Fprintln(os.Stderr, sd.hint())
		}
		return 1
	}
	if code != 201 {
		os.Stderr.Write(append(data, '\n'))
		return 1
	}
	printJSON(data)
	fmt.Fprintln(os.Stderr, "✅ 条目已录入（敏感字段已加密存储）")
	return 0
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
  keyhive version                   版本信息
  keyhive status                    检查服务/配置/令牌
  keyhive list [--category <分类>] [--stale <天>]  列出条目（遮蔽；--stale 只看超 N 天未更新）
  keyhive search <关键词> [--stale <天>]           搜索
  keyhive get <id>                  条目详情（遮蔽）
  keyhive reveal <id> <字段名>      取单字段明文（记审计）
  keyhive totp <id> [--field <字段>] 生成 6 位两步验证动态码（需 reveal 令牌，记审计）
  keyhive add --file <条目.json>    录入条目（admin 登录，--pass 或 KEYHIVE_ADMIN_PASS）
  keyhive import --file <csv> --format bitwarden|chrome [--dry-run]  批量导入
  keyhive export [--masked]         全库导出（admin；--masked 敏感值遮蔽）
  keyhive rotate-key                主密钥轮换：重加密全部条目（admin，记审计）
  keyhive mcp                       以 stdio MCP server 运行（供 AI 客户端接入）

配置: ~/.keyhive/config.json（KEYHIVE_CONFIG 环境变量可覆盖）`)
}
