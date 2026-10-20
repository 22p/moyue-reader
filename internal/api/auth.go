package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"moyue/internal/auth"
	"moyue/internal/store"
)

const (
	loginWindow    = 10 * time.Minute
	loginMaxFailed = 10
)

type userView struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	IsAdmin     bool      `json:"isAdmin"`
	Libraries   []string  `json:"libraries"`
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
		CreatedAt:   u.CreatedAt,
	}
}

func hashToken(token string) string {
	return auth.TokenHash(token)
}

func (s *Server) authState(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"needsSetup": s.store.CountUsers() == 0}
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		if sess, ok := s.store.GetSession(hashToken(cookie.Value)); ok {
			if u, err := s.store.GetUser(sess.UserID); err == nil {
				resp["user"] = viewUser(u)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.store.CountUsers() > 0 {
		writeError(w, http.StatusConflict, "系统已经初始化，请直接登录")
		return
	}
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if err := auth.ValidateUsername(req.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	display := strings.TrimSpace(req.DisplayName)
	if display == "" {
		display = req.Username
	}
	libs := make([]string, 0, len(s.store.ListLibraries()))
	for _, l := range s.store.ListLibraries() {
		libs = append(libs, l.ID)
	}
	u := &store.User{
		Username:     req.Username,
		DisplayName:  display,
		PasswordHash: hash,
		IsAdmin:      true,
		Libraries:    libs,
	}
	if err := s.store.CreateUser(u); err != nil {
		writeError(w, errStatus(err), "创建管理员失败")
		return
	}
	if err := s.issueSession(w, r, u); err != nil {
		writeError(w, http.StatusInternalServerError, "创建会话失败")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": viewUser(u)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.loginBlocked(ip) {
		writeError(w, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	u, err := s.store.GetUserByName(strings.TrimSpace(req.Username))
	if err != nil || !auth.VerifyPassword(u.PasswordHash, req.Password) {
		s.noteLoginFailure(ip)
		time.Sleep(120 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	s.clearLoginFailures(ip)
	if err := s.issueSession(w, r, u); err != nil {
		writeError(w, http.StatusInternalServerError, "创建会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(u)})
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, u *store.User) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	now := time.Now()
	sess := store.Session{
		UserID:    u.ID,
		UserAgent: truncate(r.UserAgent(), 300),
		IP:        s.clientIP(r),
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
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		_ = s.store.DeleteSession(hashToken(cookie.Value))
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(currentUser(r))})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	u := currentUser(r)
	if !auth.VerifyPassword(u.PasswordHash, req.CurrentPassword) {
		writeError(w, http.StatusBadRequest, "当前密码不正确")
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	if err := s.store.UpdateUser(u.ID, func(user *store.User) error {
		user.PasswordHash = hash
		return nil
	}); err != nil {
		writeError(w, errStatus(err), "修改密码失败")
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		_ = s.store.DeleteOtherSessions(u.ID, hashToken(cookie.Value))
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

func (s *Server) loginBlocked(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	bucket, ok := s.loginFails[ip]
	if !ok {
		return false
	}
	if time.Since(bucket.first) > loginWindow {
		delete(s.loginFails, ip)
		return false
	}
	return bucket.count >= loginMaxFailed
}

func (s *Server) noteLoginFailure(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	bucket, ok := s.loginFails[ip]
	if !ok || time.Since(bucket.first) > loginWindow {
		s.loginFails[ip] = &loginBucket{count: 1, first: time.Now()}
		return
	}
	bucket.count++
}

func (s *Server) clearLoginFailures(ip string) {
	s.loginMu.Lock()
	delete(s.loginFails, ip)
	s.loginMu.Unlock()
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
		writeError(w, errStatus(err), "撤销会话失败")
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
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	if err := s.store.DeleteOtherSessions(u.ID, hashToken(cookie.Value)); err != nil {
		writeError(w, http.StatusInternalServerError, "撤销会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
