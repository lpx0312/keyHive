package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// cmdLogin 创建/更新客户端配置（gh auth login 的对应物）：
// 已有配置做合并（仅覆盖本次提供的项），令牌输入回显遮蔽，
// 写前用 token_read 实际调一次 list 校验连通性，通过才落盘（0600）。
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	urlFlag := fs.String("url", "", "实例地址（如 http://localhost:8020）")
	tokenRead := fs.String("token-read", "", "AI 令牌（read/search 权限）")
	tokenReveal := fs.String("token-reveal", "", "AI 令牌（reveal 权限；纯查询场景可留空）")
	noVerify := fs.Bool("no-verify", false, "跳过写前的连通性校验")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	nonInteractive := "错误: 非交互环境请用 --url/--token-read/--token-reveal 提供全部配置项（或 --no-verify 跳过校验）\n"

	path := ConfigPath()
	cfg := &Config{}
	existed := false
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, cfg) != nil {
			fmt.Fprintf(os.Stderr, "错误: 现有配置文件格式错误 %s（请手工修复或删除后重试）\n", path)
			return 1
		}
		existed = true
	}
	interactive := term.IsTerminal(int(os.Stdin.Fd()))

	if *urlFlag != "" {
		u := strings.TrimRight(strings.TrimSpace(*urlFlag), "/")
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			fmt.Fprintln(os.Stderr, "错误: --url 需以 http:// 或 https:// 开头")
			return 2
		}
		cfg.BaseURL = u
	} else if cfg.BaseURL == "" {
		if !interactive {
			fmt.Fprint(os.Stderr, nonInteractive)
			return 2
		}
		cfg.BaseURL = promptLine(fmt.Sprintf("实例地址 [%s]", defaultBaseURL), defaultBaseURL)
	}

	if *tokenRead != "" {
		cfg.TokenRead = strings.TrimSpace(*tokenRead)
	} else if cfg.TokenRead == "" {
		if !interactive {
			fmt.Fprint(os.Stderr, nonInteractive)
			return 2
		}
		fmt.Print("token_read（Web UI「AI 令牌」页创建，勾选 read/search）: ")
		cfg.TokenRead = promptSecret(interactive)
	}
	if *tokenReveal != "" {
		cfg.TokenReveal = strings.TrimSpace(*tokenReveal)
	} else if cfg.TokenReveal == "" && interactive {
		fmt.Print("token_reveal（reveal/totp 需要；纯查询场景可留空）: ")
		cfg.TokenReveal = promptSecret(interactive)
	}

	if cfg.TokenRead == "" {
		fmt.Fprintln(os.Stderr, "错误: token_read 不能为空（Web UI「AI 令牌」页创建）")
		return 1
	}

	if !*noVerify {
		data, code, err := ListEntries(cfg, "")
		if err != nil {
			fmt.Fprintln(os.Stderr, "校验失败:", err)
			if sd, ok := err.(*ErrServiceDown); ok {
				fmt.Fprintln(os.Stderr, sd.hint())
			}
			fmt.Fprintln(os.Stderr, "（确认地址无误可加 --no-verify 跳过校验）")
			return 1
		}
		if code != 200 {
			fmt.Fprintf(os.Stderr, "校验失败: HTTP %d（token_read 无效或权限不足）\n", code)
			if len(data) > 0 {
				os.Stderr.Write(append(data, '\n'))
			}
			return 1
		}
		var list []map[string]any
		n := 0
		if json.Unmarshal(data, &list) == nil {
			n = len(list)
		}
		fmt.Printf("校验通过: %s 可访问，token_read 有效（可见条目 %d 条）\n", cfg.BaseURL, n)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "错误: 创建配置目录失败:", err)
		return 1
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "错误: 写入配置失败:", err)
		return 1
	}
	fmt.Printf("✅ 配置已%s: %s\n", pick(existed, "更新", "创建"), path)
	fmt.Println("keyhive status 查看详情")
	return 0
}

// promptLine 交互读一行，空输入/EOF 用默认值
func promptLine(prompt, def string) string {
	fmt.Printf("%s: ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || strings.TrimSpace(line) == "" {
		return def
	}
	return strings.TrimSpace(line)
}

// promptSecret 读令牌：终端下遮蔽回显，重定向/管道下退化为普通读
func promptSecret(masked bool) string {
	if masked {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}
