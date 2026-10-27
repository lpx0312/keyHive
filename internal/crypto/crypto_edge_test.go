package crypto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewKeyLength(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("非 32 字节应报错")
	}
	if _, err := New(make([]byte, 32)); err != nil {
		t.Fatalf("32 字节应成功: %v", err)
	}
}

func TestDecryptCorruptInputs(t *testing.T) {
	c, _ := New(deriveKey("k"))
	// 坏 base64
	if _, err := c.Decrypt(encPrefix + "!!!not-base64!!!"); err == nil {
		t.Fatal("坏 base64 应报错")
	}
	// 密文过短（< nonce+1）
	if _, err := c.Decrypt(encPrefix + "AQ"); err == nil {
		t.Fatal("过短密文应报错")
	}
	// 带前缀但内容损坏（长度足够）
	enc, _ := c.Encrypt("some-plaintext-value")
	tampered := enc[:len(enc)-4] + "AAAA"
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("篡改密文应报错")
	}
	if !strings.Contains(encPrefix, "enc:v1:") {
		t.Fatal("前缀常量异常")
	}
}

func TestLoadMasterKeyKeyfile(t *testing.T) {
	dir := t.TempDir()
	kf := filepath.Join(dir, "custom.key")
	os.WriteFile(kf, []byte("my-key-text\n"), 0o600)
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", kf)

	ks, err := LoadMasterKey(dir)
	if err != nil || ks.FromEnv || ks.Path != kf {
		t.Fatalf("KEYHIVE_KEYFILE 应生效: %+v %v", ks, err)
	}
	if string(ks.Key) != string(deriveKey("my-key-text")) {
		t.Fatal("密钥应按文件内容派生")
	}

	// KEYHIVE_KEYFILE 指向不存在的文件 → 错误
	t.Setenv("KEYHIVE_KEYFILE", filepath.Join(dir, "nope.key"))
	if _, err := LoadMasterKey(dir); err == nil {
		t.Fatal("缺失密钥文件应报错")
	}
}

func TestLoadMasterKeyExistingFile(t *testing.T) {
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", "")
	dir := t.TempDir()
	// 先生成，再复用已有文件路径分支
	LoadMasterKey(dir)
	ks, err := LoadMasterKey(dir)
	if err != nil || ks.Path != filepath.Join(dir, "master.key") {
		t.Fatalf("应复用已有 master.key: %+v %v", ks, err)
	}
	// 已有文件但读不了（改成目录遮蔽）——用坏内容直接验证 readKeyFile 的行为
	badDir := t.TempDir()
	os.MkdirAll(filepath.Join(badDir, "master.key"), 0o700) // 同名目录
	if _, err := LoadMasterKey(badDir); err == nil {
		t.Fatal("master.key 为目录时应报错")
	}
}
