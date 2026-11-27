package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"moyue/internal/auth"
	"moyue/internal/store"
)

const (
	oidcStateCookie = "moyue_oidc_state"
	oidcStateTTL    = 10 * time.Minute
	maxOIDCStates   = 2048
)

// oidcLoginState 保存一次授权码流程的临时参数，回调时一次性消费。
type oidcLoginState struct {
	nonce    string
	verifier string
	next     string
	silent   bool
	expires  time.Time
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.redirectAuthError(w, r, "disabled")
		return
	}
	state, err := auth.NewToken()
	if err != nil {
		s.redirectAuthError(w, r, "state")
		return
	}
	nonce, err := auth.NewToken()
	if err != nil {
		s.redirectAuthError(w, r, "state")
		return
	}
	verifier, err := auth.NewToken()
	if err != nil {
		s.redirectAuthError(w, r, "state")
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	// silent 用于判断身份提供商是否已有会话：prompt=none 不会弹出登录页，
	// 未登录时回调会带回 login_required，前端随后展示本地登录页。
	silent := r.URL.Query().Get("silent") == "1"

	now := time.Now()
	s.oidcMu.Lock()
	for key, pending := range s.oidcStates {
		if now.After(pending.expires) {
			delete(s.oidcStates, key)
		}
	}
	if len(s.oidcStates) >= maxOIDCStates {
		s.oidcMu.Unlock()
		s.redirectAuthError(w, r, "busy")
		return
	}
	s.oidcStates[state] = &oidcLoginState{
		nonce:    nonce,
		verifier: verifier,
		next:     next,
		silent:   silent,
		expires:  now.Add(oidcStateTTL),
	}
	s.oidcMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state,
		Path:     "/api/auth/oidc",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.isHTTPS(r),
		MaxAge:   int(oidcStateTTL.Seconds()),
	})
	prompt := ""
	if silent {
		prompt = "none"
	}
	http.Redirect(w, r, s.oidc.AuthCodeURL(s.oidcRedirectURL(r), state, nonce, verifier, prompt), http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.redirectAuthError(w, r, "disabled")
		return
	}
	query := r.URL.Query()
	state := query.Get("state")
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || state == "" || cookie.Value == "" ||
		subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		s.redirectAuthError(w, r, "state")
		return
	}

	now := time.Now()
	s.oidcMu.Lock()
	pending, ok := s.oidcStates[state]
	if ok {
		delete(s.oidcStates, state)
	}
	s.oidcMu.Unlock()
	if !ok || now.After(pending.expires) {
		s.redirectAuthError(w, r, "state")
		return
	}
	s.clearOIDCStateCookie(w, r)

	// 静默检查时身份提供商未登录会返回 login_required；这不是错误，
	// 回到本地登录页等待用户主动登录即可。
	if query.Get("error") != "" {
		if pending.silent {
			http.Redirect(w, r, withQuery(pending.next, "sso", "0"), http.StatusFound)
			return
		}
		s.redirectAuthError(w, r, "denied")
		return
	}

	code := query.Get("code")
	if code == "" {
		s.redirectAuthError(w, r, "exchange")
		return
	}
	ident, err := s.oidc.Exchange(r.Context(), s.oidcRedirectURL(r), code, pending.verifier)
	if err != nil {
		slog.Warn("failed to exchange OIDC authorization code", "err", err)
		s.redirectAuthError(w, r, "exchange")
		return
	}
	if subtle.ConstantTimeCompare([]byte(ident.Nonce), []byte(pending.nonce)) != 1 {
		slog.Warn("OIDC nonce mismatch")
		s.redirectAuthError(w, r, "verify")
		return
	}
	if strings.TrimSpace(ident.Subject) == "" {
		slog.Warn("OIDC identity is missing subject")
		s.redirectAuthError(w, r, "verify")
		return
	}

	// 管理员只由服务端 OIDC subject 白名单决定。
	isAdmin := s.oidc.IsAdminSubject(ident.Subject)
	u, err := s.store.UpsertOIDCUser(store.OIDCIdentity{
		Issuer:            ident.Issuer,
		Subject:           ident.Subject,
		Email:             ident.Email,
		DisplayName:       ident.DisplayName,
		PreferredUsername: ident.PreferredUsername,
		AvatarURL:         ident.AvatarURL,
		Admin:             isAdmin,
	})
	if err != nil {
		slog.Error("failed to store OIDC account", "err", err)
		s.redirectAuthError(w, r, "account")
		return
	}
	if err := s.issueSession(w, r, u, ident.IDToken); err != nil {
		slog.Error("failed to create OIDC session", "err", err)
		s.redirectAuthError(w, r, "session")
		return
	}
	http.Redirect(w, r, pending.next, http.StatusFound)
}

func (s *Server) requestOrigin(r *http.Request) string {
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if s.trustProxy {
		if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
			host = strings.TrimSpace(strings.Split(xfh, ",")[0])
		}
	}
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func (s *Server) oidcRedirectURL(r *http.Request) string {
	return s.requestOrigin(r) + "/api/auth/oidc/callback"
}

func (s *Server) oidcPostLogoutRedirectURL(r *http.Request) string {
	return s.requestOrigin(r) + "/api/auth/oidc/logout/callback"
}

func (s *Server) oidcLogoutCallback(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		_ = s.store.DeleteSession(hashToken(cookie.Value))
	}
	s.clearSessionCookie(w, r)
	http.Redirect(w, r, "/?logged_out=1", http.StatusFound)
}

func (s *Server) redirectAuthError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?auth_error="+url.QueryEscape(code), http.StatusFound)
}

func (s *Server) clearOIDCStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    "",
		Path:     "/api/auth/oidc",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.isHTTPS(r),
		MaxAge:   -1,
	})
}

// withQuery 给站内地址追加查询参数，保留原有查询串与 hash。
func withQuery(target, key, value string) string {
	u, err := url.Parse(target)
	if err != nil || !strings.HasPrefix(target, "/") {
		return "/?" + url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

// safeNext 只允许站内绝对路径，避免登录后被重定向到外部站点。
func safeNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") ||
		strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}
