package crypto

// 盲区/边界补测（对应 docs/review/01-review-crypto-totp.md「测试盲区」一节），
// 与 crypto_test.go / crypto_edge_test.go 已有断言不重复，全部应当通过。
// 注：key_check 被替换为明文常量的绕过路径（报告 P2-2）属已知缺陷，
// 按本轮约定不为其新增红测，故此处不出现对该形态的断言。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadMasterKeySourcePriority 三来源优先级与空白环境变量跳过行为
func TestLoadMasterKeySourcePriority(t *testing.T) {
	// 准备：KEYHIVE_KEYFILE 候选文件与 data/master.key 同时存在且内容不同
	setup := func(t *testing.T) (dataDir, keyfile string) {
		t.Helper()
		dataDir = t.TempDir()
		keyfile = filepath.Join(dataDir, "custom.key")
		if err := os.WriteFile(keyfile, []byte("custom-key-material\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "master.key"), []byte("datadir-key-material\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dataDir, keyfile
	}

	t.Run("env优先于keyfile与数据目录", func(t *testing.T) {
		dataDir, keyfile := setup(t)
		t.Setenv("KEYHIVE_MASTER_KEY", "from-env")
		t.Setenv("KEYHIVE_KEYFILE", keyfile)
		ks, err := LoadMasterKey(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if !ks.FromEnv || ks.Path != "" {
			t.Fatalf("env 应最优先且无文件路径: %+v", ks)
		}
		if string(ks.Key) != string(deriveKey("from-env")) {
			t.Fatal("密钥应按 env 值派生")
		}
	})

	t.Run("keyfile优先于数据目录", func(t *testing.T) {
		dataDir, keyfile := setup(t)
		t.Setenv("KEYHIVE_MASTER_KEY", "")
		t.Setenv("KEYHIVE_KEYFILE", keyfile)
		ks, err := LoadMasterKey(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if ks.FromEnv || ks.Path != keyfile {
			t.Fatalf("keyfile 应优先于数据目录: %+v", ks)
		}
		if string(ks.Key) != string(deriveKey("custom-key-material")) {
			t.Fatal("密钥应按 keyfile 内容派生")
		}
	})

	t.Run("空字符串MASTER_KEY视同未设置", func(t *testing.T) {
		dataDir, keyfile := setup(t)
		t.Setenv("KEYHIVE_MASTER_KEY", "")
		t.Setenv("KEYHIVE_KEYFILE", keyfile)
		ks, err := LoadMasterKey(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if ks.FromEnv || ks.Path != keyfile {
			t.Fatalf("空 env 应跳过: %+v", ks)
		}
	})

	t.Run("纯空白MASTER_KEY视同未设置", func(t *testing.T) {
		dataDir, keyfile := setup(t)
		t.Setenv("KEYHIVE_MASTER_KEY", "  \t\n  ")
		t.Setenv("KEYHIVE_KEYFILE", keyfile)
		ks, err := LoadMasterKey(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if ks.FromEnv || ks.Path != keyfile {
			t.Fatalf("纯空白 env 应跳过: %+v", ks)
		}
		if string(ks.Key) != string(deriveKey("custom-key-material")) {
			t.Fatal("密钥应按 keyfile 内容派生")
		}
	})

	t.Run("纯空白KEYFILE视同未设置", func(t *testing.T) {
		dataDir, _ := setup(t)
		t.Setenv("KEYHIVE_MASTER_KEY", "")
		t.Setenv("KEYHIVE_KEYFILE", "   ")
		ks, err := LoadMasterKey(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(dataDir, "master.key")
		if ks.FromEnv || ks.Path != want {
			t.Fatalf("纯空白 KEYFILE 应跳过: %+v", ks)
		}
		if string(ks.Key) != string(deriveKey("datadir-key-material")) {
			t.Fatal("密钥应按数据目录 master.key 内容派生")
		}
	})
}

// TestLoadMasterKeyKeyfileIsDir KEYHIVE_KEYFILE 指向目录时应报错
func TestLoadMasterKeyKeyfileIsDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "keydir")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", sub)
	if _, err := LoadMasterKey(dir); err == nil {
		t.Fatal("KEYHIVE_KEYFILE 指向目录应报错")
	}
}

// TestLoadMasterKeyDataDirUnwritable 数据目录不可写时自动生成分支应报错。
// dataDir 传普通文件路径：master.key 子路径 Stat 失败 → 走自动生成 →
// MkdirAll 对已存在的文件路径报错，可移植地模拟"不可写"。
func TestLoadMasterKeyDataDirUnwritable(t *testing.T) {
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", "")
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMasterKey(fileAsDir); err == nil {
		t.Fatal("数据目录不可写时自动生成 master.key 应报错")
	}
}

// TestKeyCheckFormat key_check 输出应为密文形态且解密为固定明文
func TestKeyCheckFormat(t *testing.T) {
	c, err := New(deriveKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	kc, err := c.KeyCheck()
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(kc) {
		t.Fatalf("key_check 应为密文形态: %s", kc)
	}
	plain, err := c.Decrypt(kc)
	if err != nil || plain != "keyhive-key-check-v1" {
		t.Fatalf("key_check 解密应为固定明文: %q %v", plain, err)
	}
}

// TestVerifyKeyCheckMismatchPaths 校验值各畸形/不匹配形态应一律不通过
func TestVerifyKeyCheckMismatchPaths(t *testing.T) {
	c, _ := New(deriveKey("k"))
	valid, err := c.KeyCheck()
	if err != nil {
		t.Fatal(err)
	}
	// 中部单字符翻转（密文体区，base64 字符集内替换，长度不变 → GCM 认证失败）
	b := []byte(valid)
	mid := len(b) / 2
	if b[mid] == 'A' {
		b[mid] = 'B'
	} else {
		b[mid] = 'A'
	}
	tampered := string(b)
	wrongPlain, err := c.Encrypt("totally-other-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		stored string
	}{
		{"空字符串", ""},
		{"普通明文", "plaintext-value"},
		{"密文形态但坏base64", encPrefix + "!!!not-base64!!!"},
		{"密文形态但长度不足", encPrefix + "AQ"},
		{"被篡改的key_check密文", tampered},
		{"加密了非校验明文", wrongPlain},
		{"前缀前有空格致无前缀", " " + valid},
	}
	for _, tc := range cases {
		if c.VerifyKeyCheck(tc.stored) {
			t.Errorf("%s: 不应通过校验 (stored=%.32s...)", tc.name, tc.stored)
		}
	}
}

// TestDecryptMalformedInputs 带前缀但载荷畸形 / 非本版本前缀的各类输入
func TestDecryptMalformedInputs(t *testing.T) {
	c, _ := New(deriveKey("k"))
	base, err := c.Encrypt("0123456789abcdefghijklmnopqrstuvwxyz0123456789")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimPrefix(base, encPrefix)
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	cases := []struct {
		name    string
		in      string
		wantErr bool
		wantOut string
	}{
		{"非本版本前缀原样透传", "enc:v2:whatever", false, "enc:v2:whatever"},
		{"仅前缀空载荷", encPrefix, true, ""},
		{"截断至过短", encPrefix + body[:8], true, ""},
		{"截断至一半", encPrefix + body[:len(body)/2/4*4], true, ""},
		{"篡改nonce区首字符", flip(base, len(encPrefix)), true, ""},
		{"篡改密文中部", flip(base, len(base)/2), true, ""},
	}
	for _, tc := range cases {
		got, err := c.Decrypt(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: 应报错, got %q", tc.name, got)
			}
		} else if err != nil || got != tc.wantOut {
			t.Errorf("%s: got %q err %v", tc.name, got, err)
		}
	}
}

// TestRoundtripLargeField MB 级大字段往返正确性（报告盲区）
func TestRoundtripLargeField(t *testing.T) {
	c, _ := New(deriveKey("k"))
	cases := []struct {
		name  string
		plain string
	}{
		{"4MB单字节模式", strings.Repeat("abcdefgh0123456789", 256*1024)},
		{"1MB多字节UTF8", strings.Repeat("密码🔐", 128*1024)},
	}
	for _, tc := range cases {
		if len(tc.plain) < 1<<20 {
			t.Fatalf("%s: 用例应达 MB 级, 实际 %d 字节", tc.name, len(tc.plain))
		}
		enc, err := c.Encrypt(tc.plain)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Errorf("%s: 解密失败: %v", tc.name, err)
			continue
		}
		if got != tc.plain {
			t.Errorf("%s: 往返不一致 (len %d vs %d)", tc.name, len(got), len(tc.plain))
		}
	}
}

// TestRoundtripBinaryPlainText 含 NUL/无效 UTF-8/超长单行等明文往返
func TestRoundtripBinaryPlainText(t *testing.T) {
	c, _ := New(deriveKey("k"))
	cases := []string{
		string([]byte{0x00, 0x01, 0x02, 0xfe, 0xff}),
		"tab\tand\r carriage",
		strings.Repeat("x", 4096),
	}
	for _, plain := range cases {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Decrypt(enc)
		if err != nil || got != plain {
			t.Errorf("往返不一致: got %q err %v", got, err)
		}
	}
}

// TestIsEncryptedVariants 前缀判定的大小写/位置边界
func TestIsEncryptedVariants(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"enc:v1:", true},
		{"enc:v1:QUJD", true},
		{"ENC:V1:QUJD", false},
		{"xenc:v1:QUJD", false},
		{" enc:v1:QUJD", false},
		{"enc:v2:QUJD", false},
	}
	for _, tc := range cases {
		if got := IsEncrypted(tc.in); got != tc.want {
			t.Errorf("IsEncrypted(%q) = %v want %v", tc.in, got, tc.want)
		}
	}
}
