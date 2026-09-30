package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := deriveKey("test-master-key")
	c, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"", "hello", "中文密码🔐", "multi\nline\nvalue"} {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatal(err)
		}
		if !IsEncrypted(enc) {
			t.Fatalf("密文缺少前缀: %s", enc)
		}
		if enc == plain {
			t.Fatal("密文等于明文")
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatal(err)
		}
		if got != plain {
			t.Fatalf("往返不一致: got %q want %q", got, plain)
		}
	}
}

func TestEncryptNotDeterministic(t *testing.T) {
	c, _ := New(deriveKey("k"))
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if a == b {
		t.Fatal("两次加密结果相同（nonce 未随机化）")
	}
}

func TestWrongKeyFails(t *testing.T) {
	c1, _ := New(deriveKey("correct"))
	c2, _ := New(deriveKey("wrong"))
	enc, _ := c1.Encrypt("secret")
	if _, err := c2.Decrypt(enc); err == nil {
		t.Fatal("错误密钥应解密失败")
	}
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	c, _ := New(deriveKey("k"))
	got, err := c.Decrypt("not-encrypted")
	if err != nil || got != "not-encrypted" {
		t.Fatalf("无前缀值应原样返回: %q %v", got, err)
	}
}

func TestKeyCheck(t *testing.T) {
	c, _ := New(deriveKey("k"))
	stored, err := c.KeyCheck()
	if err != nil {
		t.Fatal(err)
	}
	if !c.VerifyKeyCheck(stored) {
		t.Fatal("正确密钥应通过校验")
	}
	other, _ := New(deriveKey("other"))
	if other.VerifyKeyCheck(stored) {
		t.Fatal("错误密钥不应通过校验")
	}
}

func TestLoadMasterKeyAutoGenerate(t *testing.T) {
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", "")
	dir := t.TempDir()

	k1, src1, err := LoadMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "master.key")); err != nil {
		t.Fatal("应自动生成 master.key")
	}
	// 第二次加载应读到同一密钥
	k2, _, err := LoadMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range k1 {
		if k1[i] != k2[i] {
			t.Fatal("两次加载的密钥应一致")
		}
	}
	if src1 == "" {
		t.Fatal("应返回密钥来源描述")
	}
}

func TestLoadMasterKeyEnvPriority(t *testing.T) {
	t.Setenv("KEYHIVE_MASTER_KEY", "from-env")
	t.Setenv("KEYHIVE_KEYFILE", "")
	_, src, err := LoadMasterKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if src != "KEYHIVE_MASTER_KEY 环境变量" {
		t.Fatalf("环境变量应优先: %s", src)
	}
}
