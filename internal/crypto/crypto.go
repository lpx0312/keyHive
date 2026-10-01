package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// encPrefix 密文前缀，无前缀的值视为明文（兼容手工导入）
const encPrefix = "enc:v1:"

// Cipher 基于 AES-256-GCM 的字段加解密
type Cipher struct {
	aead cipher.AEAD
}

// New 用 32 字节主密钥构造
func New(masterKey []byte) (*Cipher, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("主密钥长度必须为 32 字节，实际 %d", len(masterKey))
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 明文 → enc:v1:base64(nonce+ciphertext)
func (c *Cipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// Decrypt 逆操作；输入不带前缀时原样返回（视为明文）
func (c *Cipher) Decrypt(s string) (string, error) {
	if !strings.HasPrefix(s, encPrefix) {
		return s, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, encPrefix))
	if err != nil {
		return "", err
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns+1 {
		return "", errors.New("密文长度异常")
	}
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（主密钥不匹配或数据损坏）: %w", err)
	}
	return string(plain), nil
}

// IsEncrypted 是否为密文格式
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, encPrefix)
}

// keyCheckPlain 启动校验用的固定明文
const keyCheckPlain = "keyhive-key-check-v1"

// KeyCheck 生成校验值（存 settings 表）
func (c *Cipher) KeyCheck() (string, error) {
	return c.Encrypt(keyCheckPlain)
}

// VerifyKeyCheck 校验主密钥是否与库内数据匹配
func (c *Cipher) VerifyKeyCheck(stored string) bool {
	plain, err := c.Decrypt(stored)
	return err == nil && plain == keyCheckPlain
}

// KeySource 主密钥加载结果（rotate-key 需要知道密钥来源以决定持久化方式）
type KeySource struct {
	Key     []byte
	FromEnv bool   // true=KEYHIVE_MASTER_KEY 环境变量（服务无法代写）
	Path    string // 密钥文件路径（FromEnv=false 时有效）
	Desc    string // 人类可读来源描述
}

// LoadMasterKey 按优先级加载主密钥：
// 1. KEYHIVE_MASTER_KEY 环境变量（hex/base64/任意口令，统一 SHA-256 派生 32 字节）
// 2. KEYHIVE_KEYFILE 指定的密钥文件
// 3. data/master.key：不存在则自动生成并保存（权限 0600），存在则读取
func LoadMasterKey(dataDir string) (*KeySource, error) {
	if env := strings.TrimSpace(os.Getenv("KEYHIVE_MASTER_KEY")); env != "" {
		return &KeySource{Key: deriveKey(env), FromEnv: true, Desc: "KEYHIVE_MASTER_KEY 环境变量"}, nil
	}
	if f := strings.TrimSpace(os.Getenv("KEYHIVE_KEYFILE")); f != "" {
		key, err := readKeyFile(f)
		if err != nil {
			return nil, err
		}
		return &KeySource{Key: key, Path: f, Desc: "密钥文件 " + f}, nil
	}
	path := filepath.Join(dataDir, "master.key")
	if _, err := os.Stat(path); err == nil {
		key, err := readKeyFile(path)
		if err != nil {
			return nil, err
		}
		return &KeySource{Key: key, Path: path, Desc: "密钥文件 " + path}, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	keyText := base64.StdEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(keyText+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("写入主密钥文件失败: %w", err)
	}
	return &KeySource{Key: deriveKey(keyText), Path: path, Desc: "新生成的密钥文件 " + path}, nil
}

func readKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取密钥文件失败: %w", err)
	}
	return deriveKey(strings.TrimSpace(string(data))), nil
}

// deriveKey 任意输入 → 32 字节密钥
func deriveKey(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// DeriveKeyText 导出版 deriveKey（rotate-key 生成新密钥时保持同源派生）
func DeriveKeyText(s string) []byte { return deriveKey(s) }
