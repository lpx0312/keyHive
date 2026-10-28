package totp

// 盲区/边界补测（对应 docs/review/01-review-crypto-totp.md「测试盲区」一节），
// 与 totp_test.go 已有断言不重复，全部应当通过。

import (
	"testing"
	"time"
)

// TestCodeWindowBoundaries 30s 窗口边界：窗口内恒定、跨窗口切换、纪元前与远未来
func TestCodeWindowBoundaries(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	at := func(t *testing.T, sec int64) string {
		t.Helper()
		code, err := Code(secret, time.Unix(sec, 0).UTC())
		if err != nil {
			t.Fatalf("T=%d: %v", sec, err)
		}
		return code
	}
	inWindow := at(t, 0)
	if at(t, 15) != inWindow || at(t, 29) != inWindow {
		t.Fatal("同一 30s 窗口内取值应恒定")
	}
	next := at(t, 30)
	if at(t, 59) != next {
		t.Fatal("T=30 与 T=59 同属窗口 1，取值应一致")
	}
	if next == inWindow {
		t.Fatal("相邻窗口边界两侧（T=29/T=30）应产生不同码（RFC 向量 287082/081804）")
	}
	// 纪元前：Go 整数除法向零截断，T=-1 与 T=0 同落窗口 0
	if at(t, -1) != inWindow {
		t.Fatal("T=-1 应与 T=0 同码")
	}
	// T=-30 落入 counter=uint64(-1) 回绕值，仅验证可用性不 panic
	if code, err := Code(secret, time.Unix(-30, 0).UTC()); err != nil || len(code) != 6 {
		t.Fatalf("T=-30: got %q err %v", code, err)
	}
	// 远未来（2100-01-01）不溢出
	if code, err := Code(secret, time.Unix(4102444800, 0).UTC()); err != nil || len(code) != 6 {
		t.Fatalf("T=4102444800: got %q err %v", code, err)
	}
}

// TestSecretSeparatorVariants decodeSecret 对 \n、\r\n、\t、连字符的分隔容错
func TestSecretSeparatorVariants(t *testing.T) {
	const want = "287082" // RFC 向量 T=59
	at := time.Unix(59, 0).UTC()
	variants := []string{
		"GEZDGNBVGY3TQOJQ\nGEZDGNBVGY3TQOJQ",   // 换行分组
		"GEZDGNBVGY3TQOJQ\r\nGEZDGNBVGY3TQOJQ",  // CRLF
		"GEZDGNBV\tGY3TQOJQ\tGEZDGNBV\tGY3TQOJQ", // 制表符
		"gezdgnbv-gy3tqojq-gezdgnbv-gy3tqojq",   // 小写 + 连字符
	}
	for _, v := range variants {
		got, err := Code(v, at)
		if err != nil || got != want {
			t.Errorf("变体 %q: got %s err %v", v, got, err)
		}
	}
}

// TestSecretNonASCIIRejected 全角空格/全角字母等非半角字符应报错
// （报告盲区：全角空格 U+3000 不被过滤，最终以 base32 错误暴露）
func TestSecretNonASCIIRejected(t *testing.T) {
	at := time.Unix(59, 0).UTC()
	for _, s := range []string{
		"JBSW　Y3DPEHPK3PXP",        // 含全角空格 U+3000
		"ＪＢＳＷＹ３ＤＰＥＨＰＫ３ＰＸＰ", // 全角字母/数字
		"----  \t\n",                 // 仅分隔字符 → 清洗后为空
	} {
		if _, err := Code(s, at); err == nil {
			t.Errorf("非半角/纯分隔密钥应报错: %q", s)
		}
	}
}

// TestSecretInvalidBase32Chars base32 字母表外字符（0/1/8/9 及标点）应报错
func TestSecretInvalidBase32Chars(t *testing.T) {
	at := time.Unix(59, 0).UTC()
	for _, s := range []string{"ABCDEFG0", "ABCDEFG1", "ABCDEFG8", "ABCDEFG9", "abc def!"} {
		if _, err := Code(s, at); err == nil {
			t.Errorf("非法 base32 字符应报错: %q", s)
		}
	}
}

// TestParseOTAuthEdge otpauth 解析边界：空白、大小写、缺参、控制字符
func TestParseOTAuthEdge(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  otpauth://totp/x?secret=abc  ", "abc"},                     // 首尾空白应被裁剪
		{"OTPAUTH://totp/x?secret=abc", "OTPAUTH://totp/x?secret=abc"}, // 前缀大小写敏感，视为纯密钥
		{"otpauth://totp/no-param", ""},                                // 无 secret 参数
		{"otpauth://totp/x?SECRET=abc", ""},                            // 参数名大小写敏感
		{"otpauth://totp/x?secret=ab\nc", ""},                          // 控制字符致 url.Parse 失败
	}
	for _, tc := range cases {
		if got := ParseOTAuth(tc.in); got != tc.want {
			t.Errorf("ParseOTAuth(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}
