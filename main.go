// 墨阅 · 一个轻量的网页端 EPUB 阅读器
//
//	go run . -addr :8080 -books ./books -data ./data
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"moyue/internal/api"
	"moyue/internal/store"
)

//go:embed all:web
var embedded embed.FS

func main() {
	addr := flag.String("addr", ":8080", "监听地址，例如 :8080")
	dataDir := flag.String("data", "data", "阅读数据目录（进度、书签、统计）")
	booksDir := flag.String("books", "books", "EPUB 文件存放目录")
	debug := flag.Bool("debug", false, "输出调试日志")
	trustProxy := flag.Bool("trust-proxy", false, "部署在受信任反向代理后（读取 X-Forwarded-*）")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	web, err := fs.Sub(embedded, "web")
	if err != nil {
		slog.Error("加载内置页面失败", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*booksDir, 0o755); err != nil {
		slog.Error("无法创建书库目录", "err", err, "dir", *booksDir)
		os.Exit(1)
	}
	st, err := store.Open(*dataDir)
	if err != nil {
		slog.Error("无法打开数据库", "err", err, "dir", *dataDir)
		os.Exit(1)
	}
	srv, err := api.New(st, *booksDir, web, api.Options{TrustProxy: *trustProxy})
	if err != nil {
		slog.Error("初始化服务失败", "err", err)
		os.Exit(1)
	}
	if err := srv.EnsureDefaultLibrary(); err != nil {
		slog.Error("初始化默认书库失败", "err", err)
		os.Exit(1)
	}
	srv.ScanAll()

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	displayURL := "http://localhost" + *addr
	if !strings.HasPrefix(*addr, ":") {
		displayURL = "http://" + *addr
	}

	go func() {
		slog.Info("墨阅已启动", "url", displayURL, "books", *booksDir, "db", st.Path())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("服务异常退出", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("正在关闭服务…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("关闭超时", "err", err)
	}
	if err := st.Close(); err != nil {
		slog.Error("关闭数据库失败", "err", err)
	}
}
