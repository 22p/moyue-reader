package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moyue/internal/auth"
	"moyue/internal/store"
)

type libraryView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Path      string     `json:"path,omitempty"`
	Type      string     `json:"type"`
	BookCount int        `json:"bookCount"`
	Scan      ScanStatus `json:"scan"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *Server) libraryView(l *store.Library) libraryView {
	return libraryView{
		ID:        l.ID,
		Name:      l.Name,
		Path:      l.Path,
		Type:      l.Type,
		BookCount: s.store.BookCount(l.ID),
		Scan:      s.scanSnapshot(l.ID),
		CreatedAt: l.CreatedAt,
	}
}

// listLibraries 返回当前用户可访问的书库（不含服务器路径）。
func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	out := make([]libraryView, 0)
	for _, l := range s.store.ListLibraries() {
		if !s.store.CanAccess(u, l.ID) {
			continue
		}
		out = append(out, libraryView{
			ID:        l.ID,
			Name:      l.Name,
			Type:      l.Type,
			BookCount: s.store.BookCount(l.ID),
			CreatedAt: l.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

func (s *Server) adminLibraries(w http.ResponseWriter, r *http.Request) {
	libs := s.store.ListLibraries()
	out := make([]libraryView, 0, len(libs))
	for _, l := range libs {
		out = append(out, s.libraryView(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if req.Type == "" {
		req.Type = "mixed"
	}
	if req.Name == "" || len([]rune(req.Name)) > 40 {
		writeError(w, http.StatusBadRequest, "书库名称不能为空且不超过 40 个字符")
		return
	}
	switch req.Type {
	case "mixed", "ebook", "epub", "txt", "comic", "pdf", "kindle", "audio":
	default:
		writeError(w, http.StatusBadRequest, "书库类型不受支持")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "请填写服务器上的目录路径")
		return
	}
	abs, err := filepath.Abs(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "目录路径无效")
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "目录不存在或不是文件夹："+abs)
		return
	}
	for _, l := range s.store.ListLibraries() {
		if strings.EqualFold(filepath.Clean(l.Path), filepath.Clean(abs)) {
			writeError(w, http.StatusConflict, "该目录已经被书库「"+l.Name+"」使用")
			return
		}
	}

	lib := &store.Library{Name: req.Name, Path: abs, Type: req.Type}
	if err := s.store.AddLibrary(lib); err != nil {
		writeError(w, errStatus(err), "创建书库失败")
		return
	}
	s.startScan(lib.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"library": s.libraryView(lib)})
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetLibrary(id); err != nil {
		writeError(w, http.StatusNotFound, "书库不存在")
		return
	}
	if err := s.store.DeleteLibrary(id); err != nil {
		writeError(w, errStatus(err), "删除书库失败")
		return
	}
	s.clearAllCaches()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) scanLibraryHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetLibrary(id); err != nil {
		writeError(w, http.StatusNotFound, "书库不存在")
		return
	}
	status := s.startScan(id)
	writeJSON(w, http.StatusAccepted, map[string]any{"scan": status})
}

/* ------------------------------- 用户 ------------------------------- */

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	users := s.store.ListUsers()
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, viewUser(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string   `json:"username"`
		Password    string   `json:"password"`
		DisplayName string   `json:"displayName"`
		IsAdmin     bool     `json:"isAdmin"`
		Libraries   []string `json:"libraries"`
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
	libs, err := s.validLibraries(req.Libraries)
	if err != nil {
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
	u := &store.User{
		Username:     req.Username,
		DisplayName:  display,
		PasswordHash: hash,
		IsAdmin:      req.IsAdmin,
		Libraries:    libs,
	}
	if err := s.store.CreateUser(u); err != nil {
		writeError(w, errStatus(err), "创建用户失败")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": viewUser(u)})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, err := s.store.GetUser(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	var req struct {
		DisplayName *string   `json:"displayName"`
		Password    *string   `json:"password"`
		IsAdmin     *bool     `json:"isAdmin"`
		Libraries   *[]string `json:"libraries"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}

	var libs []string
	if req.Libraries != nil {
		libs, err = s.validLibraries(*req.Libraries)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var hash string
	if req.Password != nil && *req.Password != "" {
		if err := auth.ValidatePassword(*req.Password); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		hash, err = auth.HashPassword(*req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "密码处理失败")
			return
		}
	}

	if req.IsAdmin != nil && !*req.IsAdmin && target.IsAdmin {
		admins := 0
		for _, u := range s.store.ListUsers() {
			if u.IsAdmin {
				admins++
			}
		}
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "至少需要保留一个管理员")
			return
		}
	}

	err = s.store.UpdateUser(id, func(u *store.User) error {
		if req.DisplayName != nil {
			name := strings.TrimSpace(*req.DisplayName)
			if name != "" {
				u.DisplayName = name
			}
		}
		if hash != "" {
			u.PasswordHash = hash
		}
		if req.IsAdmin != nil {
			u.IsAdmin = *req.IsAdmin
		}
		if req.Libraries != nil {
			u.Libraries = libs
		}
		return nil
	})
	if err != nil {
		writeError(w, errStatus(err), "修改用户失败")
		return
	}
	updated, _ := s.store.GetUser(id)
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(updated)})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if current := currentUser(r); current != nil && current.ID == id {
		writeError(w, http.StatusBadRequest, "不能删除当前登录的账号")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		writeError(w, errStatus(err), "删除用户失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) validLibraries(ids []string) ([]string, error) {
	known := map[string]bool{}
	for _, l := range s.store.ListLibraries() {
		known[l.ID] = true
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !known[id] {
			return nil, &badRequest{"书库不存在：" + id}
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

type badRequest struct{ msg string }

func (e *badRequest) Error() string { return e.msg }
