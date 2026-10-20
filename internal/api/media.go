package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"moyue/internal/comic"
	"moyue/internal/store"
)

type cachedComic struct {
	pages   []comic.Page
	modTime int64
	size    int64
}

// comicPages 返回漫画页列表，并按文件的 mtime/size 缓存。
func (s *Server) comicPages(b *store.Book) ([]comic.Page, error) {
	file := s.bookPath(b)
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	cached, ok := s.comicCache[b.ID]
	s.mu.RUnlock()
	if ok && cached.modTime == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.pages, nil
	}
	pages, err := comic.List(file)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.comicCache) > 8 {
		s.comicCache = map[string]cachedComic{}
	}
	s.comicCache[b.ID] = cachedComic{pages: pages, modTime: info.ModTime().UnixNano(), size: info.Size()}
	s.mu.Unlock()
	return pages, nil
}

func (s *Server) comicPage(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "cbz" && b.Type != "cbr" {
		writeError(w, http.StatusBadRequest, "这本书不是漫画包")
		return
	}
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || idx < 0 {
		writeError(w, http.StatusBadRequest, "页码无效")
		return
	}
	pages, err := s.comicPages(b)
	if err != nil || idx >= len(pages) {
		writeError(w, http.StatusNotFound, "页面不存在")
		return
	}
	data, err := comic.Read(s.bookPath(b), pages[idx].Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取页面失败")
		return
	}
	w.Header().Set("Content-Type", contentType(pages[idx].Name))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

func (s *Server) pdfFile(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "pdf" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=600")
	http.ServeFile(w, r, s.bookPath(b))
}

func (s *Server) audioFile(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "audio" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(b.File))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, s.bookPath(b))
}

func (s *Server) audioViewer(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "audio" {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy",
		"default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; img-src 'self' data:; "+
			"media-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'")
	_, _ = w.Write(s.audioViewHTML)
}

func (s *Server) pdfViewer(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "pdf" {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy",
		"default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; img-src 'self' data: blob:; "+
			"font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'")
	_, _ = w.Write(s.pdfViewHTML)
}

// pdfPageCount 使用 pdfcpu 读取页数（纯 Go，无外部程序依赖）。
func pdfPageCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := api.PageCount(ctx, f, model.NewDefaultConfiguration())
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, errors.New("PDF 页数为 0")
	}
	return n, nil
}

func (s *Server) fb2Image(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "fb2" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("name")), "#")
	fb, err := s.parseFB2(b)
	if err != nil || name == "" {
		http.NotFound(w, r)
		return
	}
	data := fb.Images[name]
	if len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", fb.ImageTypes[name])
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

func (s *Server) kindleImage(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "mobi" && b.Type != "azw" && b.Type != "azw3" && b.Type != "prc" {
		http.NotFound(w, r)
		return
	}
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || idx <= 0 {
		http.NotFound(w, r)
		return
	}
	kb, _, err := s.parseKindle(b)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	res := kb.Resources[idx]
	if res == nil || len(res.Data) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kindleMime(res.Kind))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(res.Data)
}

func kindleMime(kind string) string {
	switch kind {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}
