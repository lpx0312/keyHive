package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kdb "github.com/lpx0312/keyHive/internal/db"
)

// freeAddr 取一个当前空闲的本地地址（拿到后立即释放，交给 runServe 绑定）
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// waitReady 轮询公开的版本接口直到服务就绪
func waitReady(t *testing.T, addr string) {
	t.Helper()
	var lastErr error
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://" + addr + "/api/v1/version")
		if err == nil {
			resp.Body.Close()
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("服务未就绪: %v", lastErr)
}

// setServeEnv 隔离环境变量与数据目录
func setServeEnv(t *testing.T, addr, dataDir string) {
	t.Helper()
	t.Setenv("KEYHIVE_ADDR", addr)
	t.Setenv("KEYHIVE_DATA", dataDir)
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", "")
}

// waitDone 等 runServe 返回；超时即视为优雅停机卡死
func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅停机应返回 nil，实际 %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("优雅停机超时（15s）")
	}
}

// TestRunServeSmoke 真实 runServe(ctx)：起服务 → 探活 → cancel 优雅停机。
// 旧实现用 go runServe() 且永不返回，会泄漏 goroutine 与 db 句柄；现在可干净收尾。
func TestRunServeSmoke(t *testing.T) {
	addr := freeAddr(t)
	dataDir := filepath.Join(t.TempDir(), "srv")
	setServeEnv(t, addr, dataDir)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx) }()

	waitReady(t, addr)
	resp, err := http.Get("http://" + addr + "/api/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 128)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if resp.StatusCode != 200 || !strings.Contains(body, "version") {
		t.Fatalf("version 接口异常: %d %s", resp.StatusCode, body)
	}
	// 数据目录已生成 master.key 与库文件
	for _, f := range []string{"master.key", "keyhive.db"} {
		if _, err := os.Stat(filepath.Join(dataDir, f)); err != nil {
			t.Fatalf("%s 未生成: %v", f, err)
		}
	}

	cancel()
	waitDone(t, done)
}

// TestRunServeGracefulShutdownReleasesWAL 复现线上现象：WAL 模式下运行期
// -wal/-shm 存在；优雅停机后二者必须消失，且 WAL 中未 checkpoint 的数据不丢。
func TestRunServeGracefulShutdownReleasesWAL(t *testing.T) {
	addr := freeAddr(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	setServeEnv(t, addr, dataDir)
	dbPath := filepath.Join(dataDir, "keyhive.db")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx) }()
	waitReady(t, addr)

	// 运行期 WAL 文件必然存在
	if _, err := os.Stat(dbPath + "-wal"); err != nil {
		t.Fatalf("运行期应有 -wal: %v", err)
	}

	// 借第二个连接写一条数据，制造"尚未 checkpoint 进主库"的 WAL 帧。
	// 该连接关闭时 runServe 的连接仍开着，WAL 不会被提前删除。
	const probeKey, probeVal = "wal_probe", "alive"
	d, err := kdb.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)`, probeKey, probeVal)
	d.Close()
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	waitDone(t, done)

	// 核心断言：最后一个连接正常关闭 → checkpoint + 删除 -wal/-shm
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Errorf("停机后 %s 应被删除，实际 stat: %v", suffix, err)
		}
	}

	// WAL 里的数据必须已合并回主库
	d2, err := kdb.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	err = d2.QueryRow(`SELECT value FROM settings WHERE key = ?`, probeKey).Scan(&got)
	d2.Close()
	if err != nil || got != probeVal {
		t.Fatalf("WAL 数据未合并回主库: value=%q err=%v", got, err)
	}

	// 端口已释放
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("停机后端口未释放: %v", err)
	}
	ln.Close()
}

// TestRunServeListenFailureReturnsError 监听失败时 runServe 应返回 error
// （旧实现 log.Fatal → os.Exit 会直接杀掉进程），且 defer closeDatabase 仍执行。
func TestRunServeListenFailureReturnsError(t *testing.T) {
	// 非法端口跨平台稳定失败，不依赖 SO_REUSEADDR 的平台差异
	dataDir := filepath.Join(t.TempDir(), "data")
	setServeEnv(t, "127.0.0.1:70000", dataDir)

	err := runServe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 服务异常退出") {
		t.Fatalf("监听失败应返回包装后的 error，实际 %v", err)
	}
	dbPath := filepath.Join(dataDir, "keyhive.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, statErr := os.Stat(dbPath + suffix); !os.IsNotExist(statErr) {
			t.Errorf("失败路径也应释放数据库、删除 %s，实际 stat: %v", suffix, statErr)
		}
	}
}

// dispatch 分发逻辑（default 分支 log.Fatalf 不可测，跳过）
func TestDispatch(t *testing.T) {
	if got := dispatch(nil); got != -1 {
		t.Fatalf("无参数应 -1（继续 serve），实际 %d", got)
	}
	if got := dispatch([]string{"serve"}); got != -1 {
		t.Fatalf("serve 应 -1，实际 %d", got)
	}
	if got := dispatch([]string{"version"}); got != 0 {
		t.Fatalf("version 应 0，实际 %d", got)
	}
	if got := dispatch([]string{"-v"}); got != 0 {
		t.Fatalf("-v 应 0，实际 %d", got)
	}
	// 客户端子命令交给 cli.Run（坏配置返回 1）
	badCfg := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(badCfg, []byte("{bad"), 0o600)
	t.Setenv("KEYHIVE_CONFIG", badCfg)
	if got := dispatch([]string{"status"}); got != 1 {
		t.Fatalf("status 坏配置应 1，实际 %d", got)
	}
	// 有效配置下 help → usage → 2
	t.Setenv("KEYHIVE_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	if got := dispatch([]string{"help"}); got != 2 {
		t.Fatalf("help 应 2（usage），实际 %d", got)
	}
}
