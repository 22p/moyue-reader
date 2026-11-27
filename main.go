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
	"sync"
	"syscall"
	"time"
	"unicode"

	"moyue/internal/api"
	"moyue/internal/oidc"
	"moyue/internal/store"
)

//go:embed all:web
var embedded embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address, for example :8080")
	dataDir := flag.String("data", "data", "reading data directory (progress, bookmarks, stats)")
	booksDir := flag.String("books", "books", "directory for EPUB files")
	debug := flag.Bool("debug", false, "enable debug logging")
	trustProxy := flag.Bool("trust-proxy", false, "trust a reverse proxy (read X-Forwarded-*)")
	oidcDiscovery := flag.String("oidc-discovery", envDefault("", "MOYUE_OIDC_DISCOVERY"), "OIDC discovery URL, e.g. https://id.example.com/.well-known/openid-configuration")
	oidcClientID := flag.String("oidc-client-id", envDefault("", "MOYUE_OIDC_CLIENT_ID"), "OIDC client id")
	oidcClientSecret := flag.String("oidc-client-secret", envDefault("", "MOYUE_OIDC_CLIENT_SECRET"), "OIDC client secret (may be empty for public clients)")
	oidcAdmins := flag.String("oidc-admins", envDefault("", "MOYUE_OIDC_ADMINS"), "comma-separated admin OIDC subject allowlist")
	oidcScopes := flag.String("oidc-scopes", envDefault("openid profile email", "MOYUE_OIDC_SCOPES"), "OIDC scopes, separated by spaces or commas")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	web, err := fs.Sub(embedded, "web")
	if err != nil {
		slog.Error("failed to load embedded pages", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*booksDir, 0o755); err != nil {
		slog.Error("cannot create library directory", "err", err, "dir", *booksDir)
		os.Exit(1)
	}
	st, err := store.Open(*dataDir)
	if err != nil {
		slog.Error("cannot open database", "err", err, "dir", *dataDir)
		os.Exit(1)
	}
	oidcConfig := oidc.Config{
		DiscoveryURL: *oidcDiscovery,
		ClientID:     *oidcClientID,
		ClientSecret: *oidcClientSecret,
		Scopes:       splitList(*oidcScopes),
		AdminIDs:     splitList(*oidcAdmins),
	}
	var oidcClient *oidc.Client
	if oidcConfig.Enabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		oidcClient, err = oidc.New(ctx, oidcConfig)
		cancel()
		if err != nil {
			slog.Error("failed to initialize OIDC login", "err", err)
			os.Exit(1)
		}
		if len(oidcConfig.AdminIDs) == 0 {
			slog.Warn("OIDC admin allowlist is empty; no signed-in user will be an administrator")
		}
	} else {
		slog.Warn("-oidc-discovery is not set; sign-in will be unavailable")
	}
	srv, err := api.New(st, *booksDir, web, api.Options{TrustProxy: *trustProxy, OIDC: oidcClient})
	if err != nil {
		slog.Error("failed to initialize server", "err", err)
		os.Exit(1)
	}
	if err := srv.EnsureDefaultLibrary(); err != nil {
		slog.Error("failed to initialize the default library", "err", err)
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

	// 低频维护：清理过期会话与过旧的每日统计，空间碎片较多时回收数据库文件。
	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		run := func() {
			if err := st.Maintain(); err != nil {
				slog.Warn("database maintenance failed", "err", err)
			}
		}
		run()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()

	displayURL := "http://localhost" + *addr
	if !strings.HasPrefix(*addr, ":") {
		displayURL = "http://" + *addr
	}

	go func() {
		slog.Info("Moyue started", "url", displayURL, "books", *booksDir, "db", st.Path())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server exited unexpectedly", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown timed out", "err", err)
	}
	background.Wait()
	if err := st.Close(); err != nil {
		slog.Error("failed to close database", "err", err)
	}
}

// envDefault 返回环境变量的值；为空或未设置时使用回退值。
func envDefault(fallback, key string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
