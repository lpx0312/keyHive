package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunServeSmoke 真实 runServe：起服务（goroutine 内阻塞）→ 探活 → 版本接口可用。
// ListenAndServe 正常运行不返回，测试结束时进程退出带走 goroutine。
func TestRunServeSmoke(t *testing.T) {
	// 找一个空闲端口（Listen(":0") 拿到后关闭，交由 runServe 使用）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	// runServe 的 goroutine 会持有 keyhive.db 句柄（Windows 下 TempDir 清理会失败），
	// 故用手动目录并容忍删除失败
	root, err := os.MkdirTemp("", "keyhive-serve")
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "srv")
	t.Cleanup(func() { os.RemoveAll(root) }) // 服务句柄未释放时删除失败可容忍
	t.Setenv("KEYHIVE_ADDR", addr)
	t.Setenv("KEYHIVE_DATA", dataDir)
	t.Setenv("KEYHIVE_MASTER_KEY", "")
	t.Setenv("KEYHIVE_KEYFILE", "")

	go runServe() // 正常运行时一直阻塞

	base := "http://" + addr
	var resp *http.Response
	for i := 0; i < 50; i++ { // 等服务就绪
		resp, err = http.Get(base + "/api/v1/version")
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("服务未就绪: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 128)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if resp.StatusCode != 200 || !strings.Contains(body, "version") {
		t.Fatalf("version 接口异常: %d %s", resp.StatusCode, body)
	}
	// 数据目录已生成 master.key 与库文件
	if _, err := os.Stat(filepath.Join(dataDir, "master.key")); err != nil {
		t.Fatalf("master.key 未生成: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "keyhive.db")); err != nil {
		t.Fatalf("数据库未生成: %v", err)
	}
	_ = fmt.Sprint
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

