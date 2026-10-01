// Package totp 实现 RFC 6238 时间型一次性密码（TOTP，HMAC-SHA1，30s 窗口，6 位码），
// 兼容 Google Authenticator / Bitwarden 等主流验证器的 base32 密钥与 otpauth:// URI。
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const period = 30

// Code 生成指定时刻的 6 位动态码
func Code(secret string, t time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix() / period)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)
	// 动态截断（RFC 4226 §5.3）
	off := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// Current 当前动态码 + 本窗口剩余秒数
func Current(secret string) (string, int, error) {
	now := time.Now()
	code, err := Code(secret, now)
	if err != nil {
		return "", 0, err
	}
	return code, period - int(now.Unix()%period), nil
}

// decodeSecret base32 解码，容错：去空白、转大写、重整 padding
func decodeSecret(s string) ([]byte, error) {
	clean := strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\n' || r == '\r' || r == '\t' || r == '=' {
			return -1
		}
		return r
	}, s))
	if clean == "" {
		return nil, errors.New("TOTP 密钥为空")
	}
	if pad := len(clean) % 8; pad != 0 {
		clean += strings.Repeat("=", 8-pad)
	}
	key, err := base32.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("TOTP 密钥不是有效的 base32: %w", err)
	}
	return key, nil
}

// ParseOTAuth 解析 otpauth://totp/...?secret=XXX URI，返回 secret；
// 输入不是 otpauth URI 时原样返回（视为纯密钥）
func ParseOTAuth(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "otpauth://") {
		return v
	}
	u, err := url.Parse(v)
	if err != nil {
		return ""
	}
	return u.Query().Get("secret")
}
