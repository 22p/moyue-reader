package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"moyue/internal/auth"
	"moyue/internal/store"
)

type userView struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	IsAdmin     bool      `json:"isAdmin"`
	Libraries   []string  `json:"libraries"`
	Subject     string    `json:"subject,omitempty"`
	Email       string    `json:"email,omitempty"`
	AvatarURL   string    `json:"avatarUrl,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

func viewUser(u *store.User) userView {
	libs := u.Libraries
	if libs == nil {
		libs = []string{}
	}
	return userView{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		IsAdmin:     u.IsAdmin,
		Libraries:   append([]string{}, libs...),
		Subject:     u.OIDCSubject,
		Email:       u.Email,
		AvatarURL:   u.AvatarURL,
		CreatedAt:   u.CreatedAt,
	}
}

func hashToken(token string) string {
	return auth.TokenHash(token)
}

func (s *Server) authState(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"oidcEnabled": s.oidc != nil}
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		if sess, ok := s.store.GetSession(hashToken(cookie.Value)); ok {
			if u, err := s.store.GetUser(sess.UserID); err == nil {
				resp["user"] = viewUser(u)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, u *store.User, idToken string) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	now := time.Now()
	sess := store.Session{
		UserID:    u.ID,
		UserAgent: truncate(r.UserAgent(), 300),
		IP:        s.clientIP(r),
		IDToken:   idToken,
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now.Add(sessionTTL),
	}
	if err := s.store.PutSession(hashToken(token), sess); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.isHTTPS(r),
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	redirectURL := ""
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		key := hashToken(cookie.Value)
		if sess, ok := s.store.GetSession(key); ok && s.oidc != nil {
			redirectURL = s.oidc.LogoutURL(s.oidcPostLogoutRedirectURL(r), sess.IDToken)
		}
		_ = s.store.DeleteSession(key)
	}
	s.clearSessionCookie(w, r)
	resp := map[string]any{"ok": true}
	if redirectURL != "" {
		resp["redirectUrl"] = redirectURL
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(currentUser(r))})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			for _, part := range strings.Split(xff, ",") {
				ip := strings.TrimSpace(part)
				if net.ParseIP(ip) != nil {
					return ip
				}
			}
		}
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(ip) != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !s.trustProxy {
		return false
	}
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = strings.TrimSpace(proto[:i])
	}
	return strings.EqualFold(proto, "https")
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.isHTTPS(r),
		MaxAge:   -1,
	})
}

/* --------------------------- 登录设备管理 --------------------------- */

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	current := store.Session{}
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		current, _ = s.store.GetSession(hashToken(cookie.Value))
	}
	list := s.store.ListSessions(u.ID)
	out := make([]map[string]any, 0, len(list))
	for _, sess := range list {
		out = append(out, map[string]any{
			"id":        sess.ID,
			"userAgent": sess.UserAgent,
			"ip":        sess.IP,
			"createdAt": sess.CreatedAt,
			"lastSeen":  sess.LastSeen,
			"expiresAt": sess.ExpiresAt,
			"current":   sess.ID != "" && sess.ID == current.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id := r.PathValue("id")
	isCurrent := false
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		if cur, ok := s.store.GetSession(hashToken(cookie.Value)); ok && cur.ID == id {
			isCurrent = true
		}
	}
	if err := s.store.DeleteSessionByID(u.ID, id); err != nil {
		writeError(w, errStatus(err), "failed to revoke session")
		return
	}
	if isCurrent {
		s.clearSessionCookie(w, r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "current": isCurrent})
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := s.store.DeleteOtherSessions(u.ID, hashToken(cookie.Value)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
