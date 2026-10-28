package api

// 【回归锚点｜API 层不预填 base_url】
// 对应 docs/review/04-role-review-r1-go.md #12 收尾：aichat 包层已实现空 baseURL 时
// 按协议分派默认端点（openai → 智谱、anthropic → Anthropic 官方），但 API 层
// currentLLMConfig 曾把空 base_url 协议盲地预填为 aichat.DefaultBaseURL（智谱 OpenAI
// 端点），使包层分派在生产路径成为死代码：anthropic + 空 baseURL 时 x-api-key 仍发往
// open.bigmodel.cn，且 GET /ai-config 回显的预填值经前端保存被持久化。
// 正确行为：API 层空 baseURL 原样透传（存什么回什么），由 aichat 层按协议分派。
//
// 观测手段与 internal/aichat/endpoint_bug_test.go 相同：aichat 包内构造完整 URL、
// 无法注入 http.Client，故通过 TestMain 设置 HTTPS_PROXY 指向本地拒绝代理并记录
// CONNECT 目标 host（ProxyFromEnvironment 对 loopback 硬编码直连，包内 httptest
// mock 均为 loopback，其余测试不受影响），测试进程内不做真实外网请求。

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lpx0312/keyHive/internal/aichat"
)

var (
	proxyMu    sync.Mutex
	proxyHosts []string // 记录经代理外发的目标 host（host 或 host:port）
)

// TestMain 必须在首次 HTTP 请求前设置代理环境（ProxyFromEnvironment 会缓存环境）
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

// serveDenyProxy 记录每个外发请求的目标 host 后直接 403 拒绝
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

// assertOutboundHost 断言刚才的外发请求全部指向期望 host（代理必拒，错误本身不判断）
func assertOutboundHost(t *testing.T, name, want string) {
	t.Helper()
	hosts := takeProxyHosts()
	if len(hosts) == 0 {
		t.Fatalf("%s：未捕获任何外发请求（代理未生效？）", name)
	}
	for _, h := range hosts {
		if hn := hostnameOf(h); hn != want {
			t.Fatalf("%s：外发请求被发往 %q，应为 %q（全部目标：%v）", name, hn, want, hosts)
		}
	}
}

// TestCurrentLLMConfigNoBaseURLPrefill【单元】API 层不得预填 base_url：
// 未存 base_url 时原样为空（端点分派交给 aichat 层），显式存过的值原样透传
func TestCurrentLLMConfigNoBaseURLPrefill(t *testing.T) {
	srv, fx := newHumanServer(t)
	defer srv.Close()
	s := &Server{Store: fx.Store}

	s.putSetting(settingLLMProtocol, aichat.ProtocolAnthropic)
	cfg := s.currentLLMConfig()
	if cfg.Protocol != aichat.ProtocolAnthropic {
		t.Fatalf("协议应透传 anthropic: %q", cfg.Protocol)
	}
	if cfg.BaseURL != "" {
		t.Fatalf("API 层不应预填 base_url（应由 aichat 层按协议分派），实际预填 %q", cfg.BaseURL)
	}

	// 显式保存过的地址原样透传（不做默认值兜底，也不改写）
	s.putSetting(settingLLMBaseURL, "https://explicit.example/v1")
	if cfg = s.currentLLMConfig(); cfg.BaseURL != "https://explicit.example/v1" {
		t.Fatalf("已存 base_url 应原样透传: %q", cfg.BaseURL)
	}
}

// TestAnthropicEmptyBaseURLGoesToAnthropicEndpoint【端到端】保存 anthropic 协议且不填
// 地址：/ai-config 回显不得回填伪造端点；/ai-config/test 与 /ai-chat 的外发请求必须
// 发往 api.anthropic.com，而不是被预填到 open.bigmodel.cn
func TestAnthropicEmptyBaseURLGoesToAnthropicEndpoint(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	// 只存协议与 Key，不填 base_url
	if code, out := do(t, a, "PUT", base+"/ai-config",
		`{"protocol":"anthropic","api_key":"sk-ant-api-layer"}`); code != 200 || out["ok"] != true {
		t.Fatalf("保存 anthropic 配置失败: %d %v", code, out)
	}
	// GET 回显：base_url 空（或缺席），绝不回填任何伪造端点（存什么回什么）
	_, out := do(t, a, "GET", base+"/ai-config", "")
	if bu, ok := out["base_url"].(string); ok && strings.TrimSpace(bu) != "" {
		t.Fatalf("GET /ai-config 不得回填伪造 base_url（未存即空）: %q", bu)
	}

	// /ai-config/test：表单空值回退已保存配置 → 外发必须去 Anthropic 官方端点
	takeProxyHosts()
	do(t, a, "POST", base+"/ai-config/test", `{}`)
	assertOutboundHost(t, "ai-config/test", "api.anthropic.com")

	// /ai-chat：同样外发 anthropic 端点（代理拒绝 → 502，属预期）
	takeProxyHosts()
	do(t, a, "POST", base+"/ai-chat", `{"text":"hi"}`)
	assertOutboundHost(t, "ai-chat", "api.anthropic.com")
}

// TestOpenAIEmptyBaseURLGoesToBigModelEndpoint【对照】openai 协议 + 空 baseURL 时
// API 层透传空值后，aichat 层默认端点分派仍指向智谱 OpenAI 兼容端点（行为不回退）
func TestOpenAIEmptyBaseURLGoesToBigModelEndpoint(t *testing.T) {
	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)

	if code, out := do(t, a, "PUT", base+"/ai-config",
		`{"protocol":"openai","api_key":"sk-openai-api-layer"}`); code != 200 || out["ok"] != true {
		t.Fatalf("保存 openai 配置失败: %d %v", code, out)
	}
	takeProxyHosts()
	do(t, a, "POST", base+"/ai-config/test", `{}`)
	assertOutboundHost(t, "ai-config/test(openai)", "open.bigmodel.cn")
}

// TestExplicitBaseURLStillUsedThroughChat【对照】显式 base_url（如本地网关）不被
// 分派逻辑改写：外发仍指向显式地址（loopback mock，不经代理直接命中并记录路径）
func TestExplicitBaseURLStillUsedThroughChat(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer ts.Close()

	srv, _ := newHumanServer(t)
	defer srv.Close()
	base := srv.URL + "/api/v1"
	a := client(t)
	do(t, a, "POST", base+"/auth/login", `{"username":"admin","password":"`+adminPW+`"}`)
	if code, out := do(t, a, "PUT", base+"/ai-config",
		`{"protocol":"anthropic","base_url":`+jsonString(ts.URL)+`,"api_key":"sk-explicit"}`); code != 200 {
		t.Fatalf("保存显式 base_url 失败: %d %v", code, out)
	}
	if code, _ := do(t, a, "POST", base+"/ai-config/test", `{}`); code != 200 {
		t.Fatal("test 请求应 200")
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("显式 base_url 应原样请求 /v1/messages，实际路径 %q", gotPath)
	}
}
