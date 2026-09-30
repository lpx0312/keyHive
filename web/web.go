package web

import "embed"

// Dist 前端构建产物（npm run build 后生成；仓库内含占位 index.html）
//
//go:embed all:dist
var Dist embed.FS
