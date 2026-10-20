package api

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"moyue/internal/comic"
	"moyue/internal/epub"
	"moyue/internal/fb2"
	"moyue/internal/kindle"
	"moyue/internal/store"
	"moyue/internal/txt"
)

// EnsureDefaultLibrary 在没有书库时创建默认书库（指向 -books 目录）。
func (s *Server) EnsureDefaultLibrary() error {
	if len(s.store.ListLibraries()) > 0 {
		return nil
	}
	abs, err := filepath.Abs(s.booksDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	return s.store.AddLibrary(&store.Library{
		Name: "默认书库",
		Path: abs,
		Type: "mixed",
	})
}

// ScanAll 在后台扫描全部书库。
func (s *Server) ScanAll() {
	for _, lib := range s.store.ListLibraries() {
		s.startScan(lib.ID)
	}
}

func (s *Server) scanSnapshot(id string) ScanStatus {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if st, ok := s.scanStatus[id]; ok {
		return *st
	}
	return ScanStatus{}
}

func (s *Server) startScan(id string) ScanStatus {
	s.scanMu.Lock()
	if st, ok := s.scanStatus[id]; ok && st.Running {
		snapshot := *st
		s.scanMu.Unlock()
		return snapshot
	}
	st := &ScanStatus{Running: true}
	s.scanStatus[id] = st
	snapshot := *st
	s.scanMu.Unlock()

	go s.runScan(id)
	return snapshot
}

func (s *Server) runScan(libID string) {
	lib, err := s.store.GetLibrary(libID)
	if err != nil {
		s.finishScan(libID, 0, 0, 0, 1, "书库不存在")
		return
	}

	var added, updated, skipped, failed int
	var firstErr string
	now := time.Now()

	err = filepath.WalkDir(lib.Path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			failed++
			if firstErr == "" {
				firstErr = walkErr.Error()
			}
			return nil
		}
		if d.IsDir() {
			if p != lib.Path {
				name := d.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "@eaDir" {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !supportedFile(lib.Type, p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			failed++
			return nil
		}
		rel, err := filepath.Rel(lib.Path, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			failed++
			return nil
		}
		rel = filepath.ToSlash(rel)
		id := store.BookID(lib.ID, rel)

		existing, _ := s.store.GetBook(id)
		if existing != nil && existing.Size == info.Size() && existing.ModTime == info.ModTime().UnixNano() {
			skipped++
			s.updateScanProgress(libID, added, updated, skipped, failed)
			return nil
		}

		book, err := s.buildBook(lib, rel, p, info)
		if err != nil {
			failed++
			slog.Warn("解析书籍失败", "file", p, "err", err)
			if firstErr == "" {
				firstErr = rel + ": " + err.Error()
			}
			s.updateScanProgress(libID, added, updated, skipped, failed)
			return nil
		}
		if existing != nil {
			book.AddedAt = existing.AddedAt
			updated++
		} else {
			added++
		}
		if err := s.store.AddBook(book); err != nil {
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
		} else {
			s.clearBookCache(book.ID)
		}
		s.updateScanProgress(libID, added, updated, skipped, failed)
		return nil
	})
	if err != nil && firstErr == "" {
		firstErr = err.Error()
	}
	_ = now
	s.finishScan(libID, added, updated, skipped, failed, firstErr)
	slog.Info("书库扫描完成", "library", lib.Name, "added", added, "updated", updated, "skipped", skipped, "failed", failed)
}

func (s *Server) updateScanProgress(id string, added, updated, skipped, failed int) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if st, ok := s.scanStatus[id]; ok {
		st.Added, st.Updated, st.Skipped, st.Failed = added, updated, skipped, failed
	}
}

func (s *Server) finishScan(id string, added, updated, skipped, failed int, errMsg string) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	st, ok := s.scanStatus[id]
	if !ok {
		st = &ScanStatus{}
		s.scanStatus[id] = st
	}
	now := time.Now()
	st.Running = false
	st.Added, st.Updated, st.Skipped, st.Failed = added, updated, skipped, failed
	st.LastScan = &now
	st.Error = errMsg
}

func supportedFile(libType, p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	switch libType {
	case "epub":
		return ext == ".epub"
	case "txt":
		return ext == ".txt"
	case "ebook":
		return ext == ".epub" || ext == ".txt" || ext == ".fb2" || ext == ".mobi" || ext == ".azw" || ext == ".azw3" || ext == ".prc"
	case "comic":
		return ext == ".cbz" || ext == ".cbr"
	case "pdf":
		return ext == ".pdf"
	case "kindle":
		return ext == ".mobi" || ext == ".azw" || ext == ".azw3" || ext == ".prc"
	case "audio":
		return audioExt(ext)
	default: // mixed
		return ext == ".epub" || ext == ".txt" || ext == ".pdf" || ext == ".cbz" || ext == ".cbr" ||
			ext == ".fb2" || ext == ".mobi" || ext == ".azw" || ext == ".azw3" || ext == ".prc" || audioExt(ext)
	}
}

func audioExt(ext string) bool {
	switch ext {
	case ".mp3", ".m4a", ".m4b", ".flac", ".ogg", ".opus", ".wav":
		return true
	}
	return false
}

// mediaName 从文件名推导漫画 / PDF 的标题与作者。
func mediaName(rel string) (string, string) {
	base := strings.TrimSpace(strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)))
	title, author := base, ""
	if i := strings.Index(base, "《"); i >= 0 {
		if j := strings.Index(base[i:], "》"); j > 0 {
			title = base[i+1 : i+j]
			rest := strings.TrimSpace(base[i+j+len("》"):])
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "作者"))
			rest = strings.TrimSpace(strings.Trim(rest, ":："))
			if rest != "" {
				author = rest
			}
		}
	} else if parts := strings.SplitN(base, " - ", 2); len(parts) == 2 {
		title = strings.TrimSpace(parts[0])
		author = strings.TrimSpace(parts[1])
	}
	if title == "" {
		title = base
	}
	return title, author
}

func (s *Server) buildBook(lib *store.Library, rel, full string, info os.FileInfo) (*store.Book, error) {
	b := &store.Book{
		ID:        store.BookID(lib.ID, rel),
		LibraryID: lib.ID,
		File:      rel,
		Size:      info.Size(),
		ModTime:   info.ModTime().UnixNano(),
		AddedAt:   time.Now(),
		Language:  "zh-CN",
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".pdf":
		pages, err := pdfPageCount(full)
		if err != nil {
			return nil, err
		}
		b.Type = "pdf"
		b.Title, b.Creator = mediaName(rel)
		b.ChapterCount = pages
		b.Description = "PDF 文档 · " + strconv.Itoa(pages) + " 页"
	case ".mp3", ".m4a", ".m4b", ".flac", ".ogg", ".opus", ".wav":
		b.Type = "audio"
		b.Title, b.Creator = mediaName(rel)
		b.ChapterCount = 1
		b.Description = "有声书"
	case ".cbz", ".cbr":
		pages, err := comic.List(full)
		if err != nil {
			return nil, err
		}
		b.Type = strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), ".")
		b.Title, b.Creator = mediaName(rel)
		b.ChapterCount = len(pages)
		b.HasCover = true
		b.Description = "漫画 · " + strconv.Itoa(len(pages)) + " 页"
	case ".fb2":
		fb, err := fb2.Open(full, b.ID)
		if err != nil {
			return nil, err
		}
		b.Type = "fb2"
		b.Title = fb.Title
		b.Creator = fb.Author
		b.Language = fb.Language
		b.Publisher = fb.Publisher
		b.Description = excerpt(fb.Description, 120)
		b.PublishedAt = fb.Date
		b.HasCover = fb.Cover != ""
		b.ChapterCount = len(fb.Chapters)
	case ".mobi", ".azw", ".azw3", ".prc":
		kb, err := kindle.Open(full)
		if err != nil {
			return nil, err
		}
		chapters := kb.SplitChapters(b.ID)
		if len(chapters) == 0 {
			return nil, &badRequest{"书中没有可阅读的正文"}
		}
		b.Type = strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), ".")
		b.Title = kb.Title
		b.Creator = strings.Join(kb.Authors, "、")
		b.Language = kb.Language
		b.Publisher = kb.Publisher
		b.Description = excerpt(kb.Description, 120)
		b.HasCover = kb.Cover != nil
		b.ChapterCount = len(chapters)
	case ".epub":
		eb, err := epub.Open(full)
		if err != nil {
			return nil, err
		}
		b.Type = "epub"
		b.Title = eb.Title
		b.Creator = eb.Creator
		b.Language = eb.Language
		b.Publisher = eb.Publisher
		b.Description = eb.Description
		b.PublishedAt = eb.Date
		b.HasCover = eb.CoverHref != ""
		b.ChapterCount = len(eb.Chapters)
	case ".txt":
		tb, err := txt.ParseFile(full)
		if err != nil {
			return nil, err
		}
		b.Type = "txt"
		b.Title = tb.Title
		b.Creator = tb.Author
		b.ChapterCount = len(tb.Chapters)
		b.Description = excerpt(tb.Text, 120)
	default:
		return nil, &badRequest{"不支持的文件类型"}
	}
	if b.Title == "" {
		b.Title = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	}
	if b.Creator == "" {
		b.Creator = "佚名"
	}
	// 内容摘要用于上传去重（超大文件跳过）
	if info.Size() <= 64<<20 {
		if data, err := os.ReadFile(full); err == nil {
			b.Checksum = store.Checksum(data)
		}
	}
	return b, nil
}

func excerpt(text string, limit int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit]) + "…"
}

func (s *Server) clearBookCache(id string) {
	s.mu.Lock()
	delete(s.epubCache, id)
	delete(s.txtCache, id)
	delete(s.fb2Cache, id)
	delete(s.kindleCache, id)
	delete(s.comicCache, id)
	s.mu.Unlock()
}

func (s *Server) clearAllCaches() {
	s.mu.Lock()
	s.epubCache = map[string]cachedEPUB{}
	s.txtCache = map[string]cachedTXT{}
	s.fb2Cache = map[string]cachedFB2{}
	s.kindleCache = map[string]cachedKindle{}
	s.comicCache = map[string]cachedComic{}
	s.mu.Unlock()
}
