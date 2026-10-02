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
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lpx0312/keyHive/internal/importer"
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

// ---- edit（更新条目字段，补全密码轮换闭环） ----

// cmdEdit 用法：keyhive edit <id> <field>=<value> [<field>=<value>...]
// field 可为字段 key（仅限已存在字段）、title、category、description、ai_visible；
// 未提及的敏感字段以遮蔽值回传，由服务端保留原值（无需全量明文）。
func cmdEdit(cfg *Config, args []string) int {
	fs, user, pass := adminFlags(args)
	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "用法: keyhive edit <id> <field>=<value> [<field>=<value>...]\n示例: keyhive edit 3 password=NewP@ss")
		return 2
	}
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "错误: 未提供管理员密码（--pass 或环境变量 KEYHIVE_ADMIN_PASS）")
		return 1
	}
	id := url.PathEscape(rest[0])

	// 解析 field=value（值可含 = 号）
	pairs := map[string]string{}
	var order []string
	for _, a := range rest[1:] {
		s := strings.SplitN(a, "=", 2)
		if len(s) != 2 || s[0] == "" {
			fmt.Fprintf(os.Stderr, "错误: 参数应为 <field>=<value>，得到 %q\n", a)
			return 2
		}
		pairs[s[0]] = s[1]
		order = append(order, s[0])
	}

	// 取当前条目（admin 会话，遮蔽版——敏感值为 ***，服务端 PUT 时保留原值）
	data, code, err := adminCall(cfg, *user, *pass, http.MethodGet, "/api/v1/entries/"+id, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	if code != 200 {
		os.Stderr.Write(append(data, '\n'))
		return 1
	}
	var e map[string]any
	if err := json.Unmarshal(data, &e); err != nil {
		fmt.Fprintln(os.Stderr, "错误: 响应解析失败:", err)
		return 1
	}

	// 应用修改
	for _, k := range order {
		v := pairs[k]
		switch k {
		case "title", "category", "description":
			e[k] = v
		case "ai_visible":
			e[k] = v == "true" || v == "1"
		default:
			fields, _ := e["fields"].([]any)
			found := false
			for _, f := range fields {
				m, ok := f.(map[string]any)
				if !ok || m["key"] != k {
					continue
				}
				if b, _ := m["is_secret"].(bool); b && v == "***" {
					fmt.Fprintf(os.Stderr, "错误: 字段 %s 是敏感字段，不能显式设为 ***（省略该字段即保留原值）\n", k)
					return 2
				}
				m["value"] = v
				found = true
			}
			if !found {
				fmt.Fprintf(os.Stderr, "错误: 条目中不存在字段 %q（新增字段请用 Web 表单或 add，需要填写给 AI 的注释）\n", k)
				return 2
			}
		}
	}

	body, _ := json.Marshal(e)
	data2, code2, err := adminCall(cfg, *user, *pass, http.MethodPut, "/api/v1/entries/"+id, body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	if code2 != 200 {
		os.Stderr.Write(append(data2, '\n'))
		return 1
	}
	printJSON(data2)
	fmt.Fprintf(os.Stderr, "✅ 已更新 %d 项（未提及的敏感字段保留原值；已记审计）\n", len(order))
	return 0
}
