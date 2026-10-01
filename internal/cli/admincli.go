// admin 通道 CLI 命令：export / import / rotate-key
// 均走人用 API（admin 登录换取会话），AI 令牌不经手。
package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	"keyhive/internal/importer"
)

// adminClient admin 登录换取带会话 cookie 的 client（export/import/rotate-key/add 共用）
func adminClient(cfg *Config, user, pass string) (*http.Client, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	loginBody, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := client.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		return nil, &ErrServiceDown{Base: base}
	}
	defer resp.Body.Close()
	lb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("登录失败（HTTP %d）: %s", resp.StatusCode, string(lb))
	}
	return client, nil
}

// adminCall admin 会话发起任意请求
func adminCall(cfg *Config, user, pass, method, path string, body []byte) ([]byte, int, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	client, err := adminClient(cfg, user, pass)
	if err != nil {
		return nil, 0, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, rd)
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

// adminFlags 解析 admin 命令的公共参数（--user/--pass，KEYHIVE_ADMIN_PASS 兜底）
func adminFlags(args []string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	user := fs.String("user", "admin", "管理员用户名")
	pass := fs.String("pass", "", "管理员密码（推荐环境变量 KEYHIVE_ADMIN_PASS）")
	fs.Parse(args)
	if *pass == "" {
		*pass = strings.TrimSpace(os.Getenv("KEYHIVE_ADMIN_PASS"))
	}
	return fs, user, pass
}

// ---- export ----

func cmdExport(cfg *Config, args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	user := fs.String("user", "admin", "管理员用户名")
	pass := fs.String("pass", "", "管理员密码（推荐环境变量 KEYHIVE_ADMIN_PASS）")
	masked := fs.Bool("masked", false, "敏感值替换为 *** 后输出（用于分享结构）")
	fs.Parse(args)
	if *pass == "" {
		*pass = strings.TrimSpace(os.Getenv("KEYHIVE_ADMIN_PASS"))
	}
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "错误: 未提供管理员密码（--pass 或环境变量 KEYHIVE_ADMIN_PASS）")
		return 1
	}
	data, code, err := adminCall(cfg, *user, *pass, http.MethodGet, "/api/v1/export", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	if code != 200 {
		os.Stderr.Write(append(data, '\n'))
		return 1
	}
	if *masked {
		var list []map[string]any
		if err := json.Unmarshal(data, &list); err != nil {
			fmt.Fprintln(os.Stderr, "错误: 响应解析失败")
			return 1
		}
		for _, e := range list {
			if fields, ok := e["fields"].([]any); ok {
				for _, f := range fields {
					if m, ok := f.(map[string]any); ok {
						if sec, ok := m["is_secret"].(bool); ok && sec {
							m["value"] = "***"
						}
					}
				}
			}
		}
		out, _ := json.MarshalIndent(list, "", "  ")
		os.Stdout.Write(append(out, '\n'))
		fmt.Fprintln(os.Stderr, "⚠️  已遮蔽敏感值；本次导出已记审计")
		return 0
	}
	printJSON(data)
	fmt.Fprintln(os.Stderr, "⚠️  明文导出（含敏感值，已记审计）——请勿提交到仓库/长期留存，用完即删")
	return 0
}

// ---- import（Bitwarden / Chrome CSV）----

func cmdImport(cfg *Config, args []string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	file := fs.String("file", "", "CSV 文件路径")
	format := fs.String("format", "bitwarden", "来源格式：bitwarden | chrome")
	dryRun := fs.Bool("dry-run", false, "只预览不写入")
	user := fs.String("user", "admin", "管理员用户名")
	pass := fs.String("pass", "", "管理员密码（推荐环境变量 KEYHIVE_ADMIN_PASS）")
	fs.Parse(args)
	if *pass == "" {
		*pass = strings.TrimSpace(os.Getenv("KEYHIVE_ADMIN_PASS"))
	}
	if *file == "" {
		fmt.Fprintln(os.Stderr, "用法: keyhive import --file <csv> --format bitwarden|chrome [--dry-run]")
		return 2
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误: 读取文件失败:", err)
		return 1
	}
	entries, err := importer.Parse(*format, raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "解析到 %d 条记录\n", len(entries))
	for i, e := range entries {
		if i >= 5 {
			fmt.Fprintf(os.Stderr, "  ... 其余 %d 条略\n", len(entries)-5)
			break
		}
		user := e.Username
		if len(user) > 20 {
			user = user[:20] + "..."
		}
		fmt.Fprintf(os.Stderr, "  %d. %s (%s)\n", i+1, e.Title, user)
	}
	if *dryRun {
		fmt.Fprintln(os.Stderr, "dry-run：未写入任何数据")
		return 0
	}
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "错误: 未提供管理员密码（--pass 或环境变量 KEYHIVE_ADMIN_PASS）")
		return 1
	}
	ok, fail := 0, 0
	for _, e := range entries {
		body, _ := json.Marshal(importer.ToEntryJSON(e, sourceLabel(*format)))
		data, code, err := AddEntry(cfg, *user, *pass, body)
		if err != nil || code != 201 {
			fail++
			fmt.Fprintf(os.Stderr, "  ❌ %s: %s\n", e.Title, errOrBody(err, data))
			continue
		}
		ok++
	}
	fmt.Fprintf(os.Stderr, "导入完成：成功 %d / 失败 %d（每条已记审计）\n", ok, fail)
	if fail > 0 {
		return 1
	}
	return 0
}

func sourceLabel(format string) string {
	if strings.EqualFold(format, "bitwarden") {
		return "Bitwarden"
	}
	return "Chrome"
}

func errOrBody(err error, data []byte) string {
	if err != nil {
		return err.Error()
	}
	return string(data)
}

// ---- rotate-key ----

func cmdRotateKey(cfg *Config, args []string) int {
	_, user, pass := adminFlags(args)
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "错误: 未提供管理员密码（--pass 或环境变量 KEYHIVE_ADMIN_PASS）")
		return 1
	}
	data, code, err := adminCall(cfg, *user, *pass, http.MethodPost, "/api/v1/rotate-key", []byte("{}"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	if code != 200 {
		os.Stderr.Write(append(data, '\n'))
		return 1
	}
	printJSON(data)
	var res struct {
		Entries       int    `json:"entries"`
		KeyFile       string `json:"key_file"`
		NewMasterKey  string `json:"new_master_key"`
	}
	json.Unmarshal(data, &res)
	fmt.Fprintf(os.Stderr, "✅ 已用新主密钥重加密 %d 条条目并更新 key_check（已记审计）\n", res.Entries)
	if res.KeyFile != "" {
		fmt.Fprintf(os.Stderr, "新密钥已写入 %s —— 请把它与数据库一起纳入备份（旧备份密钥已失效！）\n", res.KeyFile)
	}
	if res.NewMasterKey != "" {
		fmt.Fprintf(os.Stderr, "⚠️  当前密钥来源是环境变量，服务无法代写。请立即更新 KEYHIVE_MASTER_KEY 为：\n%s\n并重启服务；在此之前服务内存中已使用新密钥继续运行\n", res.NewMasterKey)
	}
	return 0
}
