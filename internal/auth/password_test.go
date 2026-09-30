package auth

import "testing"

func TestPasswordRoundtrip(t *testing.T) {
	h, err := HashPassword("正确的马车电池")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("正确的马车电池", h) {
		t.Fatal("正确密码应通过")
	}
	if VerifyPassword("wrong", h) {
		t.Fatal("错误密码不应通过")
	}
}

func TestHashUniquePerCall(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("两次哈希应不同（盐随机）")
	}
}

func TestVerifyMalformed(t *testing.T) {
	if VerifyPassword("x", "not-a-hash") {
		t.Fatal("畸形哈希应返回 false 而非 panic")
	}
	if VerifyPassword("x", "$argon2id$v=19$m=65536,t=1,p=4$bad$bad") {
		t.Fatal("非法 base64 应返回 false")
	}
}
