package api

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"keyhive/internal/audit"
	"keyhive/internal/crypto"
	"keyhive/internal/model"
)

// rotateKey 主密钥轮换（admin）：新随机密钥重加密全部条目 + 更新 key_check，
// 事务成功后替换内存密钥并持久化到密钥文件；密钥来源为环境变量时不落文件，
// 一次性返回新密钥由管理员自行更新 env。
func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, 500, "生成密钥失败: "+err.Error())
		return
	}
	// 与 LoadMasterKey 的文件/env 惯例一致：存储形态为 base64 字符串，实际密钥是其 SHA-256
	keyText := base64.StdEncoding.EncodeToString(raw)
	newCipher, err := crypto.New(crypto.DeriveKeyText(keyText))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	n, err := s.Store.RotateAllEntries(newCipher)
	if err != nil {
		writeErr(w, 500, "轮换失败（数据库已回滚，旧密钥仍有效）: "+err.Error())
		return
	}

	resp := map[string]any{"ok": true, "entries": n}
	if s.Store.KeyFromEnv {
		resp["new_master_key"] = keyText
		resp["key_file"] = ""
	} else {
		path := s.Store.KeyFilePath
		if path == "" {
			path = filepath.Join(s.Store.DataDir, "master.key")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			err = os.WriteFile(path, []byte(keyText+"\n"), 0o600)
		}
		if err != nil {
			// 密钥已在内存生效但未落盘：明确告知，避免重启后回到旧密钥导致解密失败
			writeErr(w, 500, fmt.Sprintf("重加密成功但写入密钥文件 %s 失败: %v —— 服务内存已用新密钥，请立即手动写入该文件后重启", path, err))
			return
		}
		resp["key_file"] = path
	}

	uid, uname := s.actor(r)
	audit.Log(s.Store.DB, "user", uid, uname, model.ActionKeyRotate, nil,
		fmt.Sprintf(`{"entries":%d}`, n), clientIP(r))
	writeJSON(w, 200, resp)
}
