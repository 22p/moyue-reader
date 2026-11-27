package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moyue/internal/store"
)

type libraryView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Path      string     `json:"path,omitempty"`
	Type      string     `json:"type"`
	Kind      string     `json:"kind"`
	URL       string     `json:"url,omitempty"`
	Username  string     `json:"username,omitempty"`
	BookCount int        `json:"bookCount"`
	Scan      ScanStatus `json:"scan"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *Server) libraryView(l *store.Library) libraryView {
	kind := l.Kind
	if kind == "" {
		kind = "local"
	}
	v := libraryView{
		ID:        l.ID,
		Name:      l.Name,
		Type:      l.Type,
		Kind:      kind,
		BookCount: s.store.BookCount(l.ID),
		Scan:      s.scanSnapshot(l.ID),
		CreatedAt: l.CreatedAt,
	}
	if kind == "webdav" {
		v.URL = l.RemoteURL
		v.Username = l.RemoteUser
	} else {
		v.Path = l.Path
	}
	return v
}

// listLibraries 返回当前用户可访问的书库（不含服务器路径）。
func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	out := make([]libraryView, 0)
	for _, l := range s.store.ListLibraries() {
		if !s.store.CanAccess(u, l.ID) {
			continue
		}
		kind := l.Kind
		if kind == "" {
			kind = "local"
		}
		out = append(out, libraryView{
			ID:        l.ID,
			Name:      l.Name,
			Type:      l.Type,
			Kind:      kind,
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
		Name     string `json:"name"`
		Path     string `json:"path"`
		Type     string `json:"type"`
		Kind     string `json:"kind"`
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	if req.Type == "" {
		req.Type = "mixed"
	}
	if req.Kind == "" {
		req.Kind = "local"
	}
	if req.Name == "" || len([]rune(req.Name)) > 40 {
		writeError(w, http.StatusBadRequest, "library name is required and must be at most 40 characters")
		return
	}
	switch req.Type {
	case "mixed", "ebook", "epub", "txt", "comic", "pdf", "kindle", "audio":
	default:
		writeError(w, http.StatusBadRequest, "unsupported library type")
		return
	}
	var lib *store.Library
	switch req.Kind {
	case "webdav":
		raw := strings.TrimRight(strings.TrimSpace(req.URL), "/")
		if raw == "" {
			writeError(w, http.StatusBadRequest, "WebDAV URL is required")
			return
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			writeError(w, http.StatusBadRequest, "WebDAV URL must start with http:// or https://")
			return
		}
		if u.User != nil {
			writeError(w, http.StatusBadRequest, "put the username and password in the dedicated fields")
			return
		}
		for _, l := range s.store.ListLibraries() {
			if l.Kind == "webdav" && strings.EqualFold(strings.TrimRight(l.RemoteURL, "/"), raw) {
				writeError(w, http.StatusConflict, "WebDAV URL is already used by library \""+l.Name+"\"")
				return
			}
		}
		cache := filepath.Join(filepath.Dir(s.store.Path()), "webdav", store.BookID("webdav", raw))
		if err := os.MkdirAll(cache, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create WebDAV cache directory")
			return
		}
		lib = &store.Library{
			Name:       req.Name,
			Path:       cache,
			Type:       req.Type,
			Kind:       "webdav",
			RemoteURL:  raw,
			RemoteUser: strings.TrimSpace(req.Username),
			RemotePass: req.Password,
		}
	case "local":
		if req.Path == "" {
			writeError(w, http.StatusBadRequest, "server directory path is required")
			return
		}
		abs, err := filepath.Abs(req.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid directory path")
			return
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			writeError(w, http.StatusBadRequest, "directory does not exist or is not a folder: "+abs)
			return
		}
		for _, l := range s.store.ListLibraries() {
			if l.Kind != "webdav" && strings.EqualFold(filepath.Clean(l.Path), filepath.Clean(abs)) {
				writeError(w, http.StatusConflict, "directory is already used by library \""+l.Name+"\"")
				return
			}
		}
		lib = &store.Library{Name: req.Name, Path: abs, Type: req.Type, Kind: "local"}
	default:
		writeError(w, http.StatusBadRequest, "unsupported library source")
		return
	}
	if err := s.store.AddLibrary(lib); err != nil {
		writeError(w, errStatus(err), "failed to create library")
		return
	}
	s.startScan(lib.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"library": s.libraryView(lib)})
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lib, err := s.store.GetLibrary(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	if err := s.store.DeleteLibrary(id); err != nil {
		writeError(w, errStatus(err), "failed to delete library")
		return
	}
	if lib.Kind == "webdav" {
		_ = os.RemoveAll(lib.Path)
	}
	s.clearAllCaches()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) scanLibraryHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetLibrary(id); err != nil {
		writeError(w, http.StatusNotFound, "library not found")
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

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetUser(id); err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		DisplayName *string   `json:"displayName"`
		Libraries   *[]string `json:"libraries"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var libs []string
	var err error
	if req.Libraries != nil {
		libs, err = s.validLibraries(*req.Libraries)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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
		if req.Libraries != nil {
			u.Libraries = libs
		}
		return nil
	})
	if err != nil {
		writeError(w, errStatus(err), "failed to update user")
		return
	}
	updated, _ := s.store.GetUser(id)
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(updated)})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if current := currentUser(r); current != nil && current.ID == id {
		writeError(w, http.StatusBadRequest, "cannot delete the signed-in account")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		writeError(w, errStatus(err), "failed to delete user")
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
			return nil, &badRequest{"library not found: " + id}
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
