package cli

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseBitwardenCSV(t *testing.T) {
	csvData := []byte("\xEF\xBB\xBFfolder,name,login_uri,login_username,login_password,login_totp,notes\n" +
		"Work,GitHub,https://github.com,me@x.com,pw123,otpauth://totp/GitHub:me?secret=JBSWY3DPEHPK3PXP&issuer=GitHub,dev account\n" +
		",,,,,,\n" +
		"Personal,Blog,https://blog.com,alice,pw456,,\n")
	entries, err := parseBitwardenCSV(csvData)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("应解析 2 条（空行跳过），实际 %d", len(entries))
	}
	if entries[0].Title != "GitHub" || entries[0].Username != "me@x.com" || entries[0].Password != "pw123" {
		t.Fatalf("字段映射错误: %+v", entries[0])
	}
	if entries[0].TOTP == "" || entries[1].TOTP != "" {
		t.Fatalf("totp 解析错误: %+v", entries)
	}
}

func TestParseChromeCSV(t *testing.T) {
	csvData := []byte("name,url,username,password\n" +
		"Example,https://example.com,user@example.com,pw\n" +
		",,,\n")
	entries, err := parseChromeCSV(csvData)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Title != "Example" || entries[0].URL != "https://example.com" {
		t.Fatalf("Chrome 解析错误: %+v", entries)
	}
}

func TestEntryFromImport(t *testing.T) {
	e := importEntry{
		Title: "T", URL: "https://u", Username: "u", Password: "p",
		TOTP: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP", Notes: "n",
	}
	m := entryFromImport(e, "bitwarden")
	if m["category"] != "web_account" || m["title"] != "T" {
		t.Fatalf("基础映射错误: %v", m)
	}
	fields := m["fields"].([]map[string]any)
	found := map[string]bool{}
	for _, f := range fields {
		found[f["key"].(string)] = true
		if f["key"] == "totp_secret" && f["value"] != "JBSWY3DPEHPK3PXP" {
			t.Fatalf("otpauth secret 未提取: %v", f)
		}
	}
	for _, k := range []string{"url", "username", "password", "totp_secret", "notes"} {
		if !found[k] {
			t.Fatalf("缺少字段 %s", k)
		}
	}
}

func TestStaleFilterAndCount(t *testing.T) {
	old := time.Now().AddDate(0, 0, -120).UTC().Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	data, _ := json.Marshal([]staleEntry{{UpdatedAt: old}, {UpdatedAt: fresh}, {UpdatedAt: old}})

	if n := countStale(data, 90); n != 2 {
		t.Fatalf("countStale 期望 2，实际 %d", n)
	}
	filtered, err := filterStale(data, 90)
	if err != nil {
		t.Fatal(err)
	}
	var list []staleEntry
	json.Unmarshal(filtered, &list)
	if len(list) != 2 {
		t.Fatalf("filterStale 期望 2 条，实际 %d", len(list))
	}
}

func TestParseListAndSearchArgs(t *testing.T) {
	if c, d := parseListArgs([]string{"--category", "mysql", "--stale", "30"}); c != "mysql" || d != 30 {
		t.Fatalf("parseListArgs: %s %d", c, d)
	}
	if q, d := parseSearchArgs([]string{"华为", "--stale", "90"}); q != "华为" || d != 90 {
		t.Fatalf("parseSearchArgs: %s %d", q, d)
	}
	if q, _ := parseSearchArgs([]string{"--stale", "90", "x"}); q != "x" {
		t.Fatalf("关键词在后的解析: %q", q)
	}
}
