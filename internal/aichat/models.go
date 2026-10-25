package aichat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ListModels 拉取服务商可用模型清单，供前端下拉选择：
// OpenAI 协议 GET /models；Anthropic 协议 GET /v1/models（跟随分页）。
func ListModels(ctx context.Context, cfg Config) ([]string, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	var (
		ids []string
		err error
	)
	if cfg.ProtocolOrDefault() == ProtocolAnthropic {
		ids, err = listModelsAnthropic(ctx, cfg)
	} else {
		ids, err = listModelsOpenAI(ctx, cfg)
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// modelListResp OpenAI 与 Anthropic 的模型列表响应同构：{"data":[{"id":...}]}
type modelListResp struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	HasMore bool   `json:"has_more"` // Anthropic 分页
	LastID  string `json:"last_id"`
}

func fetchModelsOnce(ctx context.Context, u string, setAuth func(*http.Request)) ([]string, *modelListResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	setAuth(req)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var out modelListResp
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, nil, fmt.Errorf("响应解析失败: %w", err)
	}
	var ids []string
	for _, d := range out.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids, &out, nil
}

func listModelsOpenAI(ctx context.Context, cfg Config) ([]string, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	// 根地址带不带 /v1 的服务商都有：先按原样请求，404/405 时补 /v1 重试
	urls := []string{base + "/models"}
	if !strings.HasSuffix(base, "/v1") {
		urls = append(urls, base+"/v1/models")
	}
	setAuth := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	var firstErr error
	for i, u := range urls {
		ids, _, err := fetchModelsOnce(ctx, u, setAuth)
		if err == nil {
			if len(ids) == 0 {
				continue // 空清单也继续试下一个地址
			}
			return ids, nil
		}
		if i == 0 {
			firstErr = err
		}
		if i == len(urls)-1 && firstErr != nil {
			return nil, fmt.Errorf("%v；已尝试 %s", err, strings.Join(urls, " / "))
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("端点未返回任何模型")
}

func listModelsAnthropic(ctx context.Context, cfg Config) ([]string, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	setAuth := func(r *http.Request) {
		r.Header.Set("x-api-key", cfg.APIKey)
		r.Header.Set("anthropic-version", anthropicVersion)
	}
	var ids []string
	after := ""
	for page := 0; page < 5; page++ { // 分页兜底上限
		u := anthropicURL(cfg.BaseURL, "/models") + "?limit=1000"
		if after != "" {
			u += "&after=" + url.QueryEscape(after)
		}
		pageIDs, meta, err := fetchModelsOnce(ctx, u, setAuth)
		if err != nil {
			return nil, err
		}
		ids = append(ids, pageIDs...)
		if !meta.HasMore || meta.LastID == "" {
			break
		}
		after = meta.LastID
	}
	return ids, nil
}
