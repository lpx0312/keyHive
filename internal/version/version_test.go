package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	// 默认（无 commit）
	oldCommit := Commit
	Commit = "none"
	if s := String(); !strings.HasPrefix(s, "dev ") || strings.Contains(s, "(") {
		t.Fatalf("默认应为 dev + runtime: %q", s)
	}
	// 有 commit
	Commit = "abc1234"
	if s := String(); !strings.Contains(s, "(abc1234)") {
		t.Fatalf("应包含 commit: %q", s)
	}
	// 空 commit 视为无
	Commit = ""
	if s := String(); strings.Contains(s, "(") {
		t.Fatalf("空 commit 不应拼接: %q", s)
	}
	Commit = oldCommit
}

func TestDefaults(t *testing.T) {
	if Version == "" {
		t.Fatal("Version 不应为空")
	}
}
