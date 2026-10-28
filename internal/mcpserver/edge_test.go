package mcpserver

// 盲区/边界补测（应绿）：
//  1. stdio MCP 对畸形 JSON-RPC 的护栏行为（非 JSON / 未知 method / 缺 id），
//     锚定 mcp-go v1.1.1 的库行为，防止升级依赖时静默漂移；
//  2. kh_get 不存在条目：错误文本透传 HTTP 404。

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// startStdio 把 os.Stdin/os.Stdout 换成管道并启动 Run()，返回写入端与按行读取应答的函数
func startStdio(t *testing.T) (stdin *os.File, readLine func() string) {
	t.Helper()
	setup(t, "", nil)
	oldIn, oldOut := os.Stdin, os.Stdout
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = oldIn, oldOut })

	done := make(chan int, 1)
	go func() { done <- Run() }()
	t.Cleanup(func() {
		inW.Close()
		select {
		case rc := <-done:
			if rc != 0 && rc != 1 {
				t.Errorf("Run 退出码异常: %d", rc)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run 应在 stdin EOF 后退出")
		}
	})

	br := bufio.NewReader(outR)
	readLine = func() string {
		t.Helper()
		type res struct {
			line string
			err  error
		}
		ch := make(chan res, 1)
		go func() {
			line, err := br.ReadString('\n')
			ch <- res{line, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("读取 MCP 应答失败: %v", r.err)
			}
			return r.line
		case <-time.After(5 * time.Second):
			t.Fatal("5 秒内未读到 MCP 应答")
			return ""
		}
	}
	return inW, readLine
}

func TestMCPMalformedJSONRPC(t *testing.T) {
	stdin, readLine := startStdio(t)

	// 1) 非 JSON 文本：应回 JSON-RPC PARSE_ERROR(-32700)，且连接存活
	stdin.WriteString("this is not json at all\n")
	line := readLine()
	if !strings.Contains(line, "-32700") {
		t.Fatalf("非 JSON 输入应回 PARSE_ERROR(-32700): %s", line)
	}

	// 2) 未知 method：应回 METHOD_NOT_FOUND(-32601) 并回显 id
	stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"no/such/method"}` + "\n")
	line = readLine()
	if !strings.Contains(line, "-32601") || !strings.Contains(line, `"id":2`) {
		t.Fatalf("未知 method 应回 METHOD_NOT_FOUND(-32601) 并回显 id:2: %s", line)
	}

	// 3) 缺 id（notification）：不应产生任何应答；紧随其后的 ping 应正常应答，
	//    证明服务既没把 notification 当请求应答、也没因此挂掉
	stdin.WriteString(`{"jsonrpc":"2.0","method":"no/such/notification"}` + "\n")
	stdin.WriteString(`{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n")
	line = readLine()
	if !strings.Contains(line, `"id":3`) {
		t.Fatalf("缺 id 的消息不应有应答，读到的下一条应是 ping(id:3) 的应答: %s", line)
	}

	// 4) 错误的 jsonrpc 版本：应回 INVALID_REQUEST(-32600)
	stdin.WriteString(`{"jsonrpc":"1.0","id":4,"method":"ping"}` + "\n")
	line = readLine()
	if !strings.Contains(line, "-32600") || !strings.Contains(line, `"id":4`) {
		t.Fatalf("错误 jsonrpc 版本应回 INVALID_REQUEST(-32600): %s", line)
	}
}

func TestMCPGetNonexistentEntry(t *testing.T) {
	// kh_get 不存在条目：服务端 404 → 错误文本透传给调用方
	setup(t, goodCfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"条目不存在"}`))
	}))

	res, err := toolGet(context.Background(), callArgs(t, map[string]any{"id": "999"}))
	if err != nil {
		t.Fatal(err)
	}
	txt := resultText(t, res)
	if !strings.Contains(txt, "HTTP 404") || !strings.Contains(txt, "条目不存在") {
		t.Fatalf("kh_get 不存在条目应透传 HTTP 404 与服务端错误: %s", txt)
	}
}
