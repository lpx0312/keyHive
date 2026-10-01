package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"keyhive/internal/audit"
	"keyhive/internal/importer"
	"keyhive/internal/model"
	"keyhive/internal/totp"
	"keyhive/internal/version"
)

// versionInfo 版本信息（未认证，登录页/关于均可显示）
func (s *Server) versionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"version": version.Version})
}

// genTOTP 生成条目两步验证动态码（登录用户；secret 在服务端解密计算、不出库）
// POST /entries/{id}/totp  body: {"field":""}（空=自动定位 totp 字段）
func (s *Server) genTOTP(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(chi.URLParam(r, "id"))
	var req struct {
		Field string `json:"field"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	e, err := s.Store.GetEntry(id)
	if err != nil {
		writeErr(w, 404, "条目不存在")
		return
	}
	field := req.Field
	if field == "" {
		for _, f := range e.Fields {
			if containsFold(f.Key, "totp") || containsFold(f.Description, "TOTP") || containsFold(f.Description, "两步验证") {
				field = f.Key
				break
			}
		}
	}
	f := e.FieldByKey(field)
	if f == nil || f.Value == "" {
		writeErr(w, 404, "未找到 TOTP 字段（可在请求体指定 field）")
		return
	}
	code, remain, err := totp.Current(totp.ParseOTAuth(f.Value))
	if err != nil {
		writeErr(w, 400, "字段值不是有效的 TOTP 密钥: "+err.Error())
		return
	}
	uid, uname := s.actor(r)
	eid := e.ID
	audit.Log(s.Store.DB, "user", uid, uname, model.ActionTOTPGen, &eid,
		`{"title":"`+e.Title+`","field":"`+f.Key+`"}`, clientIP(r))
	writeJSON(w, 200, map[string]any{"code": code, "remaining": remain, "period": 30})
}

// importCSV Web 端 CSV 导入（admin）：dry_run 预览，否则逐条入库
// POST /import  body: {"format":"bitwarden|chrome","csv":"...","dry_run":bool}
func (s *Server) importCSV(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format string `json:"format"`
		CSV    string `json:"csv"`
		DryRun bool   `json:"dry_run"`
	}
	if err := readBody(r, &req); err != nil || req.CSV == "" {
		writeErr(w, 400, "请求体应为 {format, csv, dry_run}")
		return
	}
	entries, err := importer.Parse(req.Format, []byte(req.CSV))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	source := "Chrome"
	if req.Format == "bitwarden" {
		source = "Bitwarden"
	}
	if req.DryRun {
		preview := make([]map[string]string, 0, len(entries))
		for i, e := range entries {
			if i >= 20 {
				break
			}
			preview = append(preview, map[string]string{"title": e.Title, "username": e.Username})
		}
		writeJSON(w, 200, map[string]any{"count": len(entries), "preview": preview})
		return
	}

	uid, uname := s.actor(r)
	ok, fail := 0, []string{}
	for _, e := range entries {
		body, _ := json.Marshal(importer.ToEntryJSON(e, source))
		var entry model.Entry
		json.Unmarshal(body, &entry)
		if err := s.Store.CreateEntry(&entry); err != nil {
			fail = append(fail, e.Title+"（"+err.Error()+"）")
			continue
		}
		ok++
	}
	audit.Log(s.Store.DB, "user", uid, uname, model.ActionImport, nil,
		fmt.Sprintf(`{"source":"%s","ok":%d,"fail":%d}`, source, ok, len(fail)), clientIP(r))
	writeJSON(w, 200, map[string]any{"ok": ok, "fail": fail})
}

// ---- 轮换提醒阈值设置 ----

const settingStaleDays = "stale_days"

func (s *Server) getStaleDays(w http.ResponseWriter, r *http.Request) {
	days := 90
	var v string
	if err := s.Store.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, settingStaleDays).Scan(&v); err == nil {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			days = n
		}
	}
	writeJSON(w, 200, map[string]int{"stale_days": days})
}

func (s *Server) putStaleDays(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StaleDays int `json:"stale_days"`
	}
	if err := readBody(r, &req); err != nil || req.StaleDays < 1 || req.StaleDays > 3650 {
		writeErr(w, 400, "stale_days 应为 1-3650 的天数")
		return
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingStaleDays, strconv.Itoa(req.StaleDays)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int{"stale_days": req.StaleDays})
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
