package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// dispatch 安全路径表驱动（不触达 cli.Run 的网络/配置，也不触达 default 的 Fatal）
func TestDispatchTable(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"无参", nil, -1},
		{"serve", []string{"serve"}, -1},
		{"serve 带额外参数", []string{"serve", "--x", "y"}, -1},
		{"version", []string{"version"}, 0},
		{"-v", []string{"-v"}, 0},
		{"--version", []string{"--version"}, 0},
	}
	for _, c := range cases {
		if got := dispatch(c.args); got != c.want {
			t.Errorf("%s: dispatch(%v) 应 %d，实际 %d", c.name, c.args, c.want, got)
		}
	}
}

// default 分支走 log.Fatalf（进程直接退出，return 2 不可达），只能用子进程测。
// log.Fatalf 固定以退出码 1 结束，这里锁定真实行为并确认提示语。
func TestDispatchUnknownCommand(t *testing.T) {
	if os.Getenv("KEYHIVE_TEST_FATAL_DISPATCH") == "1" {
		dispatch([]string{"no-such-cmd"}) // log.Fatalf → os.Exit(1)，不会返回
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDispatchUnknownCommand$", "-test.count=1")
	cmd.Env = append(os.Environ(), "KEYHIVE_TEST_FATAL_DISPATCH=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("未知子命令应导致子进程非零退出，实际 0，输出: %s", out)
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("应收到退出错误: %v", err)
	}
	if code := ee.ExitCode(); code != 1 {
		t.Fatalf("log.Fatalf 固定退出码 1（dispatch 的 return 2 不可达），实际 %d", code)
	}
	if !strings.Contains(string(out), "未知子命令") || !strings.Contains(string(out), "no-such-cmd") {
		t.Fatalf("退出输出应包含提示: %s", out)
	}
}

// envOr 三分支：未设置/空/纯空白 → 默认值；有值 → trim 后返回
func TestEnvOrBranches(t *testing.T) {
	const key = "KEYHIVE_TEST_ENVOR"
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		}
	})

	if got := envOr(key, "def"); got != "def" {
		t.Fatalf("未设置应回默认值: %q", got)
	}
	for _, v := range []string{"", "   ", " \t "} {
		if err := os.Setenv(key, v); err != nil {
			t.Fatal(err)
		}
		if got := envOr(key, "def"); got != "def" {
			t.Fatalf("空白值 %q 应回默认值，实际 %q", v, got)
		}
	}
	if err := os.Setenv(key, "host:1"); err != nil {
		t.Fatal(err)
	}
	if got := envOr(key, "def"); got != "host:1" {
		t.Fatalf("正常值应原样返回: %q", got)
	}
	if err := os.Setenv(key, "  host:1  "); err != nil {
		t.Fatal(err)
	}
	if got := envOr(key, "def"); got != "host:1" {
		t.Fatalf("带空白值应 trim: %q", got)
	}
}
