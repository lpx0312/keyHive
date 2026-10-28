package aichat

// 【缺陷复现测试｜预期红】
// 对应 docs/review/01-review-ai-integration.md P2-4（internal/aichat/aichat.go:213-215 →
// anthropic.go:189-191）：Chat/ChatStream 入口把空 BaseURL 统一填成 DefaultBaseURL
// （智谱 OpenAI 端点）后再按协议分派，anthropic.go 里 `if cfg.BaseURL == ""` 的
// Anthropic 官方默认端点是死代码。protocol=anthropic 且 baseURL 为空时，
// 请求（连同 x-api-key）会被发往 OpenAI 端点。
// 正确行为：anthropic 协议空 baseURL 应请求 https://api.anthropic.com。
//
// 观测手段：Chat 在包内构造完整 URL，无法注入 http.Client，故通过 TestMain 设置
// HTTPS_PROXY 指向本地拒绝代理并记录 CONNECT 目标 host（Go 的 ProxyFromEnvironment
// 对 loopback 地址硬编码直连，包内其余 httptest 测试不受影响）。

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

var (
	proxyMu    sync.Mutex
	proxyHosts []string // 记录经代理外发的目标 host（host 或 host:port）
)

// TestMain 在任何测试前启动本地拒绝代理并设置环境变量：
// 必须在首次 HTTP 请求前设置（http.ProxyFromEnvironment 会缓存环境）。
func TestMain(m *testing.M) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Stderr.WriteString("启动测试代理失败: " + err.Error() + "\n")
		os.Exit(1)
	}
	go serveDenyProxy(ln)
	os.Setenv("HTTPS_PROXY", "http://"+ln.Addr().String())
	os.Setenv("https_proxy", "http://"+ln.Addr().String())
	os.Setenv("NO_PROXY", "")
	os.Setenv("no_proxy", "")
	os.Exit(m.Run())
}

// serveDenyProxy 记录每个外发请求的目标 host 后直接 403 拒绝（测试进程内不做真实外网请求）
func serveDenyProxy(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			req, err := http.ReadRequest(bufio.NewReader(c))
			if err != nil {
				return
			}
			host := req.URL.Host
			if host == "" {
				host = req.Host
			}
			proxyMu.Lock()
			proxyHosts = append(proxyHosts, host)
			proxyMu.Unlock()
			c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
		}(c)
	}
}

func takeProxyHosts() []string {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	hs := proxyHosts
	proxyHosts = nil
	return hs
}

func hostnameOf(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// TestBug_AnthropicEmptyBaseURLUsesAnthropicEndpoint：anthropic 协议 + 空 baseURL 时，
// 最终请求必须发往 api.anthropic.com，而不是被填成 OpenAI 默认端点
func TestBug_AnthropicEmptyBaseURLUsesAnthropicEndpoint(t *testing.T) {
	assertAnthropicEndpoint := func(name string, call func(ctx context.Context) error) {
		t.Helper()
		takeProxyHosts() // 清空
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = call(ctx) // 代理拒绝必失败，错误本身不判断；这里只断言请求去向
		hosts := takeProxyHosts()
		if len(hosts) == 0 {
			t.Fatalf("%s：未捕获任何外发请求（代理未生效？）", name)
		}
		for _, h := range hosts {
			if hn := hostnameOf(h); hn != "api.anthropic.com" {
				t.Fatalf("%s：空 baseURL 的 anthropic 请求被发往 %q，应使用 Anthropic 官方端点 api.anthropic.com（外发目标：%v）",
					name, hn, hosts)
			}
		}
	}

	cfg := Config{Protocol: ProtocolAnthropic, APIKey: "sk-ant-test-key", Model: "claude-sonnet-4-5"}
	assertAnthropicEndpoint("ChatStream", func(ctx context.Context) error {
		_, err := ChatStream(ctx, cfg, "sys", nil, "hi", cbWith(nil), nil)
		return err
	})
	assertAnthropicEndpoint("Chat", func(ctx context.Context) error {
		_, err := Chat(ctx, cfg, "sys", nil, "hi", cbWith(nil))
		return err
	})
}

// 对照组（应绿）：显式 baseURL 仍按原样请求，不被改写
func TestExplicitBaseURLUntouched(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	t.Cleanup(ts.Close)

	_, err := Chat(context.Background(), Config{Protocol: ProtocolAnthropic, BaseURL: ts.URL, APIKey: "k", Model: "c"},
		"sys", nil, "hi", cbWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("显式 baseURL 请求路径不符: %s", gotPath)
	}
}
