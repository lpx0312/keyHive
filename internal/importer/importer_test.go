package importer

import "testing"

func TestParseBitwardenCSV(t *testing.T) {
	csvData := []byte("\xEF\xBB\xBFfolder,name,login_uri,login_username,login_password,login_totp,notes\n" +
		"Work,GitHub,https://github.com,me@x.com,pw123,otpauth://totp/GitHub:me?secret=JBSWY3DPEHPK3PXP&issuer=GitHub,dev account\n" +
		",,,,,,\n" +
		"Personal,Blog,https://blog.com,alice,pw456,,\n")
	entries, err := Parse("bitwarden", csvData)
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
	entries, err := Parse("chrome", csvData)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Title != "Example" || entries[0].URL != "https://example.com" {
		t.Fatalf("Chrome 解析错误: %+v", entries)
	}
}

func TestParseUnknownFormat(t *testing.T) {
	if _, err := Parse("1password", nil); err == nil {
		t.Fatal("未知格式应报错")
	}
}

func TestToEntryJSON(t *testing.T) {
	e := Entry{
		Title: "T", URL: "https://u", Username: "u", Password: "p",
		TOTP: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP", Notes: "n",
	}
	m := ToEntryJSON(e, "Bitwarden")
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
		if f["key"] == "password" && f["is_secret"] != true {
			t.Fatal("密码必须标记敏感")
		}
	}
	for _, k := range []string{"url", "username", "password", "totp_secret", "notes"} {
		if !found[k] {
			t.Fatalf("缺少字段 %s", k)
		}
	}
}

func TestToEntryJSONSkipsEmpty(t *testing.T) {
	m := ToEntryJSON(Entry{Title: "x", Password: "p"}, "Chrome")
	fields := m["fields"].([]map[string]any)
	if len(fields) != 1 {
		t.Fatalf("空字段应跳过，实际 %d 个", len(fields))
	}
}
