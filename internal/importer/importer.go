// Package importer 解析 Bitwarden / Chrome 密码导出 CSV 为 keyHive 条目
// （CLI import 与 Web 导入端点共用）。
package importer

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"keyhive/internal/totp"
)

// Entry 解析后的待导入条目
type Entry struct {
	Title    string
	URL      string
	Username string
	Password string
	TOTP     string
	Notes    string
}

// Parse 按 format（bitwarden|chrome）解析 CSV 字节流（自动剥 UTF-8 BOM）
func Parse(format string, data []byte) ([]Entry, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	switch strings.ToLower(format) {
	case "bitwarden":
		return parseCSV(data, func(cols map[string]string) (Entry, bool) {
			name := cols["name"]
			if name == "" && cols["login_username"] == "" && cols["login_password"] == "" {
				return Entry{}, false
			}
			if name == "" {
				name = cols["login_uri"]
			}
			return Entry{
				Title: name, URL: cols["login_uri"], Username: cols["login_username"],
				Password: cols["login_password"], TOTP: cols["login_totp"], Notes: cols["notes"],
			}, true
		})
	case "chrome":
		return parseCSV(data, func(cols map[string]string) (Entry, bool) {
			if cols["name"] == "" && cols["password"] == "" {
				return Entry{}, false
			}
			return Entry{Title: cols["name"], URL: cols["url"], Username: cols["username"], Password: cols["password"]}, true
		})
	default:
		return nil, fmt.Errorf("未知格式（bitwarden | chrome）: %s", format)
	}
}

// parseCSV 通用：表头列名 → 行 map → 过滤映射
func parseCSV(data []byte, mapRow func(map[string]string) (Entry, bool)) ([]Entry, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // 各行列数可能不齐
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("读取表头失败: %w", err)
	}
	var out []Entry
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取行失败: %w", err)
		}
		cols := map[string]string{}
		for i, h := range header {
			if i < len(rec) {
				cols[strings.TrimSpace(h)] = rec[i]
			}
		}
		if e, ok := mapRow(cols); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// ToEntryJSON 映射为 keyHive 条目（字段名与 web_account 模板对齐），
// 返回可直接 POST /entries 的对象
func ToEntryJSON(e Entry, source string) map[string]any {
	fields := []map[string]any{}
	add := func(key, desc, val string, secret bool, multiline ...bool) {
		if strings.TrimSpace(val) == "" {
			return
		}
		t := "text"
		if len(multiline) > 0 && multiline[0] {
			t = "multiline"
		}
		fields = append(fields, map[string]any{
			"key": key, "description": desc, "type": t, "is_secret": secret, "value": val,
		})
	}
	add("url", "网址", e.URL, false)
	add("username", "用户名/邮箱", e.Username, false)
	add("password", "登录密码", e.Password, true)
	if e.TOTP != "" {
		secret := totp.ParseOTAuth(e.TOTP) // Bitwarden 常见 otpauth:// URI，提取纯密钥
		add("totp_secret", "两步验证 TOTP 密钥（keyhive totp 命令可直接生成动态码）", secret, true)
	}
	add("notes", "备注", e.Notes, false, true)
	return map[string]any{
		"title":       e.Title,
		"category":    "web_account",
		"description": fmt.Sprintf("从 %s CSV 导入 @ %s", source, time.Now().Format("2006-01-02")),
		"ai_visible":  true,
		"fields":      fields,
	}
}
