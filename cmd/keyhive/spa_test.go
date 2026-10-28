package main

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/lpx0312/keyHive/web"
)

// spaHandler 目录形路径与回退边界（补 TestSPAHandlerDirect 未覆盖的形态）：
// 依赖 embed.FS 对带斜杠路径 Stat 必然失败来回退，这一隐式行为在此锁定
func TestSPAHandlerDirPathsAndFallback(t *testing.T) {
	dist, _ := fs.Sub(web.Dist, "dist")
	h := spaHandler(dist)

	get := func(path string) *respRecorder {
		req, _ := http.NewRequest("GET", path, nil)
		w := newRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// /assets（目录、无斜杠）：Stat 成功不回退 → FileServer 301 到 assets/，无缓存头
	w := get("/assets")
	if w.code != 301 || w.Header().Get("Location") != "assets/" {
		t.Fatalf("/assets 应 301 到 assets/: code=%d loc=%q", w.code, w.Header().Get("Location"))
	}
	if cc := w.Header().Get("Cache-Control"); cc != "" {
		t.Fatalf("/assets 301 不应带缓存头: %q", cc)
	}
	// /assets/（带斜杠）：Stat 必失败 → 回退 index.html + no-cache
	w = get("/assets/")
	if w.code != 200 || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("/assets/ 应回退 no-cache: code=%d cc=%q", w.code, w.Header().Get("Cache-Control"))
	}
	if !strings.Contains(w.body.String(), "<!DOCTYPE html") {
		t.Fatalf("/assets/ 回退应为 index.html: %.40q", w.body.String())
	}
	// /index.html 直访：命中入口 → no-cache，FileServer 规范化 301 到 ./
	w = get("/index.html")
	if w.code != 301 || w.Header().Get("Location") != "./" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("/index.html 应 301 ./ 且 no-cache: code=%d loc=%q cc=%q",
			w.code, w.Header().Get("Location"), w.Header().Get("Cache-Control"))
	}
	// 深层前端路由未命中：回退 index.html + no-cache
	w = get("/deep/route/x")
	if w.code != 200 || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("未命中路径应回退 no-cache: code=%d cc=%q", w.code, w.Header().Get("Cache-Control"))
	}
	if !strings.Contains(w.body.String(), "<!DOCTYPE html") {
		t.Fatalf("未命中路径回退应为 index.html: %.40q", w.body.String())
	}
}
