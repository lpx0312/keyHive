package crypto

// ── 预期红测试（缺陷复现）────────────────────────────────────────────
// 对应缺陷（docs/review/01-review-crypto-totp.md 发现清单 P1）：
// internal/crypto/crypto.go:134-140 readKeyFile 对 TrimSpace 后为空的
// 密钥文件内容不做校验、直接 deriveKey。当 data/master.key 存在但为
// 空文件（0 字节，运维 touch 占位/写入截断等）或纯空白时，LoadMasterKey
// 走"文件存在"分支静默接受，主密钥退化为 SHA-256("") 公开常量 e3b0c442...，
// 加密等同失效且无任何告警。
// 正确行为：返回 error 拒绝启动。
// 修复前 TestBug_EmptyKeyfileRejected 应为 FAIL；随缺陷修复转绿。
// ─────────────────────────────────────────────────────────────────────

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBug_EmptyKeyfileRejected(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"零字节文件", ""},
		{"纯空白文件", " \n\t \n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KEYHIVE_MASTER_KEY", "")
			t.Setenv("KEYHIVE_KEYFILE", "")
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "master.key"), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadMasterKey(dir); err == nil {
				t.Fatalf("缺陷：空 master.key（content=%q）被静默接受并派生为公开常量密钥，应返回 error 拒绝启动", tc.content)
			}
		})
	}
}
