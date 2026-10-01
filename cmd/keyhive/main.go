package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"keyhive/internal/aiapi"
	"keyhive/internal/api"
	"keyhive/internal/auth"
	"keyhive/internal/cli"
	"keyhive/internal/crypto"
	kdb "keyhive/internal/db"
	"keyhive/internal/mcpserver"
	"keyhive/internal/store"
	"keyhive/internal/version"
	"keyhive/web"
)

func main() {
	// 子命令：无参数或 serve 启动服务；list/.../rotate-key 为客户端；mcp 为 stdio MCP server
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			// 继续启动服务
		case "list", "search", "get", "reveal", "status", "add", "totp", "export", "import", "rotate-key", "help", "-h", "--help":
			os.Exit(cli.Run(os.Args[1:]))
		case "version", "-v", "--version":
			fmt.Println(version.String())
			return
		case "mcp":
			os.Exit(mcpserver.Run())
		default:
			log.Fatalf("未知子命令: %s（keyhive help 查看用法）", os.Args[1])
		}
	}
	runServe()
}

func runServe() {
	addr := envOr("KEYHIVE_ADDR", ":8020")
	dataDir := envOr("KEYHIVE_DATA", "./data")

	// 主密钥与库分离；库内 settings.key_check 用于校验二者匹配
	keySrc, err := crypto.LoadMasterKey(dataDir)
	if err != nil {
		log.Fatalf("加载主密钥失败: %v", err)
	}
	cipherBox, err := crypto.New(keySrc.Key)
	if err != nil {
		log.Fatalf("初始化加密失败: %v", err)
	}
	log.Printf("keyHive %s | 主密钥来源: %s", version.String(), keySrc.Desc)

	database, err := kdb.Open(dataDir + "/keyhive.db")
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer database.Close()
	if err := ensureKeyCheck(database, cipherBox); err != nil {
		log.Fatalf("%v", err)
	}

	st := &store.Store{
		DB:          database,
		Cipher:      cipherBox,
		DataDir:     dataDir,
		KeyFilePath: keySrc.Path,
		KeyFromEnv:  keySrc.FromEnv,
	}
	human := &api.Server{Store: st}
	ai := &aiapi.Server{Store: st}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, req)
			if strings.HasPrefix(req.URL.Path, "/api/") {
				log.Printf("%s %s %s", req.Method, req.URL.Path, time.Since(start).Round(time.Millisecond))
			}
		})
	})

	// 人用 API
	r.Route("/api/v1", human.Routes)

	// AI API：不同端点不同 scope
	r.Route("/api/v1/ai", func(ar chi.Router) {
		ar.Group(func(g chi.Router) {
			g.Use(auth.RequireToken(database, "read"))
			g.Get("/entries", ai.ListEntries)
			g.Get("/entries/{id}", ai.GetEntry)
		})
		ar.Group(func(g chi.Router) {
			g.Use(auth.RequireToken(database, "search"))
			g.Get("/search", ai.Search)
		})
		ar.Group(func(g chi.Router) {
			g.Use(auth.RequireToken(database, "reveal"))
			g.Post("/entries/{id}/reveal", ai.Reveal)
		})
	})

	// SPA 静态资源（embed），未命中的路径回退 index.html
	dist, _ := fs.Sub(web.Dist, "dist")
	r.Handle("/*", spaHandler(dist))

	log.Printf("keyHive 启动完成: http://localhost%s (数据目录 %s)", addr, dataDir)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}

// ensureKeyCheck 首次写入校验值；已有则校验主密钥匹配
func ensureKeyCheck(db *sql.DB, c *crypto.Cipher) error {
	var stored string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = 'key_check'`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		kc, err := c.KeyCheck()
		if err != nil {
			return err
		}
		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('key_check', ?)`, kc)
		return err
	}
	if err != nil {
		return err
	}
	if !c.VerifyKeyCheck(stored) {
		return errors.New("主密钥与数据库内 key_check 不匹配：密钥已变更或数据目录错误。请用原密钥启动，或清空数据目录重新初始化")
	}
	return nil
}

func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dist, path); err != nil {
			// 前端路由路径 → 回退到 SPA 入口
			r.URL.Path = "/"
		}
		// index.html 禁缓存：assets 文件名带 hash 可长缓存，但入口必须每次校验，
		// 否则发版后浏览器仍按旧 index.html 加载旧资源（联动/修复"不生效"的根因）
		if r.URL.Path == "/" || path == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
