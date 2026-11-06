package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/lpx0312/keyHive/internal/aiapi"
	"github.com/lpx0312/keyHive/internal/api"
	"github.com/lpx0312/keyHive/internal/auth"
	"github.com/lpx0312/keyHive/internal/cli"
	"github.com/lpx0312/keyHive/internal/crypto"
	kdb "github.com/lpx0312/keyHive/internal/db"
	"github.com/lpx0312/keyHive/internal/mcpserver"
	"github.com/lpx0312/keyHive/internal/store"
	"github.com/lpx0312/keyHive/internal/version"
	"github.com/lpx0312/keyHive/web"
)

func main() {
	// 子命令：无参数或 serve 启动服务；list/.../rotate-key 为客户端；mcp 为 stdio MCP server
	if code := dispatch(os.Args[1:]); code >= 0 {
		os.Exit(code)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServe(ctx); err != nil {
		log.Fatal(err)
	}
}

// dispatch 子命令分发：返回 -1 表示继续启动服务
func dispatch(args []string) int {
	if len(args) == 0 {
		return -1
	}
	switch args[0] {
	case "serve":
		return -1
	case "login", "list", "search", "get", "reveal", "status", "add", "edit", "totp", "export", "import", "rotate-key", "help", "-h", "--help":
		return cli.Run(args)
	case "version", "-v", "--version":
		fmt.Println(version.String())
		return 0
	case "mcp":
		return mcpserver.Run()
	default:
		log.Fatalf("未知子命令: %s（keyhive help 查看用法）", args[0])
		return 2
	}
}

// shutdownTimeout 优雅停机等待活跃请求（含 SSE 长流）的上限。
// 8s < Docker 默认 10s stop grace period，超时后强制 Close 并让 runServe 正常返回，
// 避免被 SIGKILL 打断 defer closeDatabase。
const shutdownTimeout = 8 * time.Second

// runServe 启动 HTTP 服务，直到 ctx 被取消（SIGINT/SIGTERM）或监听失败。
// 与旧实现的差别：不再用 log.Fatal 中途 os.Exit，退出路径统一走 defer closeDatabase，
// 保证 SQLite 最后一个连接正常关闭 → WAL checkpoint、-wal/-shm 不再残留。
// ctx 由调用方注入，测试可直接 cancel 验证优雅停机。
func runServe(ctx context.Context) error {
	addr := envOr("KEYHIVE_ADDR", ":8020")
	dataDir := envOr("KEYHIVE_DATA", "./data")

	srv, db, err := buildServer(dataDir)
	if err != nil {
		return err
	}
	defer closeDatabase(db)
	log.Printf("keyHive 启动完成: http://localhost%s (数据目录 %s)", addr, dataDir)

	srv.Addr = addr
	// 用 srv.ListenAndServe 让 buildServer 里设置的超时生效（http.ListenAndServe 会另建零配置 server）
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("优雅停机超时，强制关闭连接: %v", err)
			_ = srv.Close()
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
		return nil
	}
}

// closeDatabase 关闭前把 WAL 合并回主库并截断，避免停机后 -wal/-shm 残留。
// checkpoint 失败也不影响数据安全：WAL 会在下次启动时自动回放；
// 真正删除这两个文件的是随后最后一个连接的正常关闭。
func closeDatabase(db *sql.DB) {
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("WAL checkpoint 失败（下次启动自动回放）: %v", err)
	}
	if err := db.Close(); err != nil {
		log.Printf("关闭数据库失败: %v", err)
	}
}

// buildServer 组装完整服务（数据库 + 主密钥 + 全部路由），runServe 与测试共用；
// 返回的 *sql.DB 由调用方负责关闭
func buildServer(dataDir string) (*http.Server, *sql.DB, error) {
	// 主密钥与库分离；库内 settings.key_check 用于校验二者匹配
	keySrc, err := crypto.LoadMasterKey(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("加载主密钥失败: %w", err)
	}
	cipherBox, err := crypto.New(keySrc.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化加密失败: %w", err)
	}
	log.Printf("keyHive %s | 主密钥来源: %s", version.String(), keySrc.Desc)

	database, err := kdb.Open(dataDir + "/keyhive.db")
	if err != nil {
		return nil, nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if err := ensureKeyCheck(database, cipherBox); err != nil {
		database.Close()
		return nil, nil, err
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

	// 请求体大小限制挂在顶层：/api/v1（人用）与 /api/v1/ai（Bearer token AI API）
	// 子树统一覆盖（后者不经过 human.Routes，此前为限制盲区）；静态资源无请求体，无害
	r.Use(api.LimitBody)

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

	// ReadHeaderTimeout 防慢速头部攻击，IdleTimeout 回收闲置连接；
	// 不设 WriteTimeout：会把 SSE 长响应（AI 聊天流）在中途切断（前端审查已注明）
	return &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, database, nil
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
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dist, path); err != nil {
			// 前端路由路径 → 回退到 SPA 入口
			r.URL.Path = "/"
		}
		// index.html 禁缓存：assets 文件名带 hash 可长缓存，但入口必须每次校验，
		// 否则发版后浏览器仍按旧 index.html 加载旧资源（联动/修复"不生效"的根因）。
		// embed 文件无 modtime，http.FileServer 不会发 Last-Modified/ETag，
		// 不显式设置缓存头的话浏览器每次都全量重拉资源
		if r.URL.Path == "/" || path == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
	// 仅静态资源开 gzip（1.8MB JS 压到 ~450KB；API/SSE 不走这个 handler，不受影响）
	return middleware.Compress(5)(h)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
