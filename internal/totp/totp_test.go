package totp

import (
	"testing"
	"time"
)

// RFC 6238 附录 B 官方测试向量（SHA1，密钥原始字节 "12345678901234567890"，
// base32 形式 GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ）；8 位码取后 6 位即验证器显示值
func TestRFC6238Vectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := Code(secret, time.Unix(c.unix, 0).UTC())
		if err != nil {
			t.Fatalf("T=%d: %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("T=%d: got %s want %s", c.unix, got, c.want)
		}
	}
}

func TestSecretBase32Tolerance(t *testing.T) {
	// 同一密钥的多种写法应产生相同结果
	variants := []string{
		"GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"gezdgnbvgy3tqojqgezdgnbvgy3tqojq", // 小写
		"GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ", // 空格分组
		"GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ======",  // 多余 padding
	}
	want := "287082"
	at := time.Unix(59, 0).UTC()
	for _, v := range variants {
		got, err := Code(v, at)
		if err != nil || got != want {
			t.Errorf("变体 %q: got %s err %v", v, got, err)
		}
	}
}

func TestCurrent(t *testing.T) {
	code, remain, err := Current("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	if err != nil || len(code) != 6 {
		t.Fatalf("Current: %q %d %v", code, remain, err)
	}
	if remain < 1 || remain > 30 {
		t.Fatalf("剩余秒数异常: %d", remain)
	}
}

func TestParseOTAuth(t *testing.T) {
	cases := []struct{ in, want string }{
		{"otpauth://totp/example.com:alice?secret=JBSWY3DPEHPK3PXP&issuer=Example", "JBSWY3DPEHPK3PXP"},
		{"otpauth://totp/x?secret=abc", "abc"},
		{"JBSWY3DPEHPK3PXP", "JBSWY3DPEHPK3PXP"}, // 纯密钥原样返回
		{"", ""},
	}
	for _, c := range cases {
		if got := ParseOTAuth(c.in); got != c.want {
			t.Errorf("ParseOTAuth(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestInvalidSecret(t *testing.T) {
	if _, _, err := Current(""); err == nil {
		t.Fatal("空密钥应报错")
	}
	if _, _, err := Current("!!!不是base32!!!"); err == nil {
		t.Fatal("非法 base32 应报错")
	}
}
