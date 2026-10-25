package aichat

// Preset 内置服务商预设（参考 cc-switch）：选中后自动填协议/地址/常用模型，
// 「获取模型列表」再从服务商拉全量。模型 ID 会随官方更新过期，仅作快捷入口。
type Preset struct {
	Name     string   `json:"name"`
	Protocol string   `json:"protocol"` // openai | anthropic
	BaseURL  string   `json:"base_url"`
	Models   []string `json:"models"`
	KeyHint  string   `json:"key_hint,omitempty"` // API Key 获取地提示
}

// Presets 内置预设：OpenAI 兼容协议在前，Anthropic 协议在后
var Presets = []Preset{
	// ---- OpenAI 兼容协议 ----
	{Name: "智谱 GLM", Protocol: ProtocolOpenAI, BaseURL: "https://open.bigmodel.cn/api/paas/v4",
		Models:  []string{"glm-4.6", "glm-4.5", "glm-4.5-air", "glm-4.5-flash"},
		KeyHint: "open.bigmodel.cn 控制台的 API Key"},
	{Name: "DeepSeek", Protocol: ProtocolOpenAI, BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-chat", "deepseek-reasoner"},
		KeyHint: "platform.deepseek.com 的 API Key"},
	{Name: "阿里云百炼（通义千问）", Protocol: ProtocolOpenAI, BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Models:  []string{"qwen3-max", "qwen-max", "qwen-plus", "qwen-turbo"},
		KeyHint: "阿里云百炼 API Key（sk- 开头）"},
	{Name: "火山方舟（豆包）", Protocol: ProtocolOpenAI, BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
		Models:  []string{"doubao-seed-1-6-250615", "doubao-1-5-pro-32k-250115"},
		KeyHint: "火山引擎方舟 API Key"},
	{Name: "月之暗面 Kimi", Protocol: ProtocolOpenAI, BaseURL: "https://api.moonshot.cn/v1",
		Models:  []string{"kimi-k2-0905-preview", "kimi-k2-turbo-preview", "moonshot-v1-8k"},
		KeyHint: "platform.moonshot.cn 的 API Key"},
	{Name: "MiniMax", Protocol: ProtocolOpenAI, BaseURL: "https://api.minimaxi.com/v1",
		Models:  []string{"MiniMax-M2", "MiniMax-Text-01"},
		KeyHint: "MiniMax 开放平台 API Key"},
	{Name: "硅基流动 SiliconFlow", Protocol: ProtocolOpenAI, BaseURL: "https://api.siliconflow.cn/v1",
		Models:  []string{"deepseek-ai/DeepSeek-V3", "Qwen/Qwen2.5-72B-Instruct", "THUDM/GLM-4-9B-0414"},
		KeyHint: "cloud.siliconflow.cn 的 API Key"},
	{Name: "OpenRouter", Protocol: ProtocolOpenAI, BaseURL: "https://openrouter.ai/api/v1",
		Models:  []string{"openai/gpt-4o", "anthropic/claude-sonnet-4.5", "deepseek/deepseek-chat"},
		KeyHint: "openrouter.ai 的 API Key（sk-or- 开头）"},
	{Name: "OpenAI 官方", Protocol: ProtocolOpenAI, BaseURL: "https://api.openai.com/v1",
		Models:  []string{"gpt-4o", "gpt-4.1", "gpt-4.1-mini", "o3"},
		KeyHint: "platform.openai.com 的 API Key（sk- 开头）"},
	{Name: "xAI Grok", Protocol: ProtocolOpenAI, BaseURL: "https://api.x.ai/v1",
		Models:  []string{"grok-4", "grok-3", "grok-3-mini"},
		KeyHint: "console.x.ai 的 API Key"},
	{Name: "Ollama（本机）", Protocol: ProtocolOpenAI, BaseURL: "http://localhost:11434/v1",
		Models:  []string{"qwen3", "llama3", "glm-4"},
		KeyHint: "本机 Ollama 无需 Key，可任意填写"},
	{Name: "LM Studio（本机）", Protocol: ProtocolOpenAI, BaseURL: "http://localhost:1234/v1",
		Models:  []string{},
		KeyHint: "本机 LM Studio 无需 Key，模型取决于本地加载"},
	// ---- Anthropic 协议 ----
	{Name: "Anthropic 官方", Protocol: ProtocolAnthropic, BaseURL: "https://api.anthropic.com",
		Models:  []string{"claude-sonnet-4-5", "claude-haiku-4-5", "claude-opus-4-1"},
		KeyHint: "console.anthropic.com 的 API Key（sk-ant- 开头）"},
	{Name: "智谱 GLM（Anthropic 兼容）", Protocol: ProtocolAnthropic, BaseURL: "https://open.bigmodel.cn/api/anthropic",
		Models:  []string{"glm-4.6", "glm-4.5"},
		KeyHint: "open.bigmodel.cn 控制台的 API Key"},
	{Name: "Kimi（Anthropic 兼容）", Protocol: ProtocolAnthropic, BaseURL: "https://api.moonshot.cn/anthropic",
		Models:  []string{"kimi-k2-0905-preview"},
		KeyHint: "platform.moonshot.cn 的 API Key"},
}
