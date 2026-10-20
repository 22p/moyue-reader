// Package api 提供 EPUB / TXT 书库的 HTTP 接口与静态页面服务。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"moyue/internal/epub"
	"moyue/internal/fb2"
	"moyue/internal/kindle"
	"moyue/internal/store"
	"moyue/internal/tts"
	"moyue/internal/txt"
)

const sessionCookie = "moyue_session"

const sessionTTL = 30 * 24 * time.Hour

type contextKey struct{}

var userKey contextKey

// ScanStatus 是某个书库的扫描状态。
type ScanStatus struct {
	Running  bool       `json:"running"`
	LastScan *time.Time `json:"lastScan,omitempty"`
	Added    int        `json:"added"`
	Updated  int        `json:"updated"`
	Skipped  int        `json:"skipped"`
	Failed   int        `json:"failed"`
	Error    string     `json:"error,omitempty"`
}

type cachedEPUB struct {
	book    *epub.Book
	modTime int64
	size    int64
}

type cachedTXT struct {
	book    *txt.Book
	modTime int64
	size    int64
}

type cachedFB2 struct {
	book    *fb2.Book
	modTime int64
	size    int64
}

type cachedKindle struct {
	book     *kindle.Book
	chapters []kindle.Chapter
	modTime  int64
	size     int64
}

// Options 是服务端的可选配置。
type Options struct {
	// TrustProxy 表示服务部署在受信任的反向代理之后，可读取
	// X-Forwarded-Proto / X-Forwarded-For / X-Forwarded-Host。
	TrustProxy bool
}

// Server 聚合依赖并提供 http.Handler。
type Server struct {
	trustProxy    bool
	store         *store.Store
	booksDir      string
	web           fs.FS
	index         []byte
	pdfViewHTML   []byte
	audioViewHTML []byte
	manifest      []byte
	sw            []byte
	offline       []byte
	ttsClient     *tts.Client

	mu          sync.RWMutex
	epubCache   map[string]cachedEPUB
	txtCache    map[string]cachedTXT
	fb2Cache    map[string]cachedFB2
	kindleCache map[string]cachedKindle
	comicCache  map[string]cachedComic
	scanStatus  map[string]*ScanStatus
	scanMu      sync.Mutex

	setupMu    sync.Mutex
	loginMu    sync.Mutex
	loginFails map[string]*loginBucket
}

type loginBucket struct {
	count int
	first time.Time
}

// New 构造 Server。web 应当是包含 index.html 与 assets/ 的只读文件系统。
func New(st *store.Store, booksDir string, web fs.FS, opts ...Options) (*Server, error) {
	cfg := Options{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	index, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return nil, err
	}
	pdfViewHTML, err := fs.ReadFile(web, "pdfview.html")
	if err != nil {
		return nil, err
	}
	audioViewHTML, err := fs.ReadFile(web, "audioview.html")
	if err != nil {
		return nil, err
	}
	manifest, err := fs.ReadFile(web, "manifest.webmanifest")
	if err != nil {
		return nil, err
	}
	sw, err := fs.ReadFile(web, "sw.js")
	if err != nil {
		return nil, err
	}
	offline, err := fs.ReadFile(web, "offline.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		trustProxy:    cfg.TrustProxy,
		store:         st,
		booksDir:      booksDir,
		web:           web,
		index:         index,
		pdfViewHTML:   pdfViewHTML,
		audioViewHTML: audioViewHTML,
		manifest:      manifest,
		sw:            sw,
		offline:       offline,
		ttsClient:     tts.NewClient(filepath.Join(filepath.Dir(st.Path()), "tts")),
		epubCache:     map[string]cachedEPUB{},
		txtCache:      map[string]cachedTXT{},
		fb2Cache:      map[string]cachedFB2{},
		kindleCache:   map[string]cachedKindle{},
		comicCache:    map[string]cachedComic{},
		scanStatus:    map[string]*ScanStatus{},
		loginFails:    map[string]*loginBucket{},
	}, nil
}

// Handler 返回配置好中间件的路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "time": time.Now().Format(time.RFC3339)})
	})

	// 认证
	mux.HandleFunc("GET /api/auth/state", s.authState)
	mux.HandleFunc("POST /api/auth/setup", s.setup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/me", s.protect(s.me))
	mux.HandleFunc("POST /api/auth/password", s.protect(s.changePassword))
	mux.HandleFunc("GET /api/auth/sessions", s.protect(s.listSessions))
	mux.HandleFunc("DELETE /api/auth/sessions/{id}", s.protect(s.revokeSession))
	mux.HandleFunc("POST /api/auth/sessions/revoke-others", s.protect(s.revokeOtherSessions))

	// 书库（已登录）
	mux.HandleFunc("GET /api/libraries", s.protect(s.listLibraries))
	mux.HandleFunc("GET /api/books", s.protect(s.listBooks))
	mux.HandleFunc("POST /api/books", s.requireAdmin(s.uploadBook))
	mux.HandleFunc("GET /api/books/{id}", s.protect(s.bookDetail))
	mux.HandleFunc("DELETE /api/books/{id}", s.requireAdmin(s.deleteBook))
	mux.HandleFunc("GET /api/books/{id}/file", s.protect(s.downloadBook))
	mux.HandleFunc("GET /api/books/{id}/cover", s.protect(s.bookCover))
	mux.HandleFunc("GET /api/books/{id}/chapter/{index}", s.protect(s.chapter))
	mux.HandleFunc("GET /api/books/{id}/page/{index}", s.protect(s.comicPage))
	mux.HandleFunc("GET /api/books/{id}/fb2image", s.protect(s.fb2Image))
	mux.HandleFunc("GET /api/books/{id}/kindleimage/{index}", s.protect(s.kindleImage))
	mux.HandleFunc("GET /api/books/{id}/pdf", s.protect(s.pdfFile))
	mux.HandleFunc("GET /api/books/{id}/pdfview", s.protect(s.pdfViewer))
	mux.HandleFunc("GET /api/books/{id}/audioview", s.protect(s.audioViewer))
	mux.HandleFunc("GET /api/books/{id}/audio", s.protect(s.audioFile))
	mux.HandleFunc("GET /api/books/{id}/resource", s.protect(s.resource))
	mux.HandleFunc("PUT /api/books/{id}/progress", s.protect(s.saveProgress))
	mux.HandleFunc("DELETE /api/books/{id}/progress", s.protect(s.resetProgress))
	mux.HandleFunc("POST /api/books/{id}/read", s.protect(s.addReading))
	mux.HandleFunc("POST /api/books/{id}/bookmarks", s.protect(s.addBookmark))
	mux.HandleFunc("DELETE /api/books/{id}/bookmarks/{bookmarkID}", s.protect(s.deleteBookmark))
	mux.HandleFunc("POST /api/books/{id}/annotations", s.protect(s.addAnnotation))
	mux.HandleFunc("PATCH /api/books/{id}/annotations/{annotationID}", s.protect(s.updateAnnotation))
	mux.HandleFunc("DELETE /api/books/{id}/annotations/{annotationID}", s.protect(s.deleteAnnotation))
	mux.HandleFunc("GET /api/books/{id}/search", s.protect(s.searchBook))
	mux.HandleFunc("POST /api/books/{id}/tts", s.protect(s.ttsSpeak))
	mux.HandleFunc("GET /api/tts/status", s.protect(s.ttsStatus))
	mux.HandleFunc("GET /api/stats", s.protect(s.stats))

	// 管理端
	mux.HandleFunc("GET /api/admin/libraries", s.requireAdmin(s.adminLibraries))
	mux.HandleFunc("POST /api/admin/libraries", s.requireAdmin(s.createLibrary))
	mux.HandleFunc("DELETE /api/admin/libraries/{id}", s.requireAdmin(s.deleteLibrary))
	mux.HandleFunc("POST /api/admin/libraries/{id}/scan", s.requireAdmin(s.scanLibraryHandler))
	mux.HandleFunc("GET /api/admin/users", s.requireAdmin(s.adminUsers))
	mux.HandleFunc("POST /api/admin/users", s.requireAdmin(s.createUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.requireAdmin(s.updateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.requireAdmin(s.deleteUser))
	mux.HandleFunc("GET /api/admin/settings/tts", s.requireAdmin(s.adminGetTTS))
	mux.HandleFunc("PUT /api/admin/settings/tts", s.requireAdmin(s.adminPutTTS))
	mux.HandleFunc("POST /api/admin/settings/tts/test", s.requireAdmin(s.adminTestTTS))

	assets, err := fs.Sub(s.web, "assets")
	if err != nil {
		slog.Error("加载静态资源失败", "err", err)
	} else {
		fileServer := http.FileServer(http.FS(assets))
		mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			fileServer.ServeHTTP(w, r)
		})))
	}

	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(s.manifest)
	})

	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = w.Write(s.sw)
	})

	mux.HandleFunc("GET /offline.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(s.offline)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "接口不存在")
			return
		}
		if r.URL.Path != "/" && path.Ext(r.URL.Path) != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(s.index)
	})

	return recoverer(logger(securityHeaders(s.csrfGuard(mux))))
}

/* ------------------------------ 中间件 ------------------------------ */

func (s *Server) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		key := hashToken(cookie.Value)
		sess, ok := s.store.GetSession(key)
		if !ok {
			writeError(w, http.StatusUnauthorized, "登录已过期")
			return
		}
		if time.Since(sess.LastSeen) > 5*time.Minute {
			s.store.TouchSession(key, time.Now())
		}
		user, err := s.store.GetUser(sess.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "账号不存在")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.protect(func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r); u == nil || !u.IsAdmin {
			writeError(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r)
	})
}

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

/* ------------------------------ 辅助 ------------------------------ */

// bookPath 返回书籍的绝对路径。
func (s *Server) bookPath(b *store.Book) string {
	lib, err := s.store.GetLibrary(b.LibraryID)
	if err != nil {
		return ""
	}
	return filepath.Join(lib.Path, filepath.FromSlash(b.File))
}

func (s *Server) canRead(u *store.User, b *store.Book) bool {
	return u != nil && s.store.CanAccess(u, b.LibraryID)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("写出 JSON 失败", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// csrfGuard 对会改变状态的请求做同源校验。SameSite=Lax 已经拦住大多数
// 跨站表单，这里再补一层 Origin 检查。
func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host == "" || !s.sameHost(u.Host, r) {
					writeError(w, http.StatusForbidden, "跨站请求已被拒绝")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) sameHost(originHost string, r *http.Request) bool {
	host := r.Host
	if s.trustProxy {
		if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
			host = strings.TrimSpace(strings.Split(xfh, ",")[0])
		}
	}
	return strings.EqualFold(originHost, host)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		if !strings.HasPrefix(r.URL.Path, "/api/books/") || !strings.Contains(r.URL.Path, "/chapter/") {
			h.Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
					"script-src 'self'; font-src 'self' data:; connect-src 'self'; media-src 'self' blob:; "+
					"frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

func logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "cost", time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("请求处理 panic", "err", rec, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func errStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, store.ErrLastAdmin):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
