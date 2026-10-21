package api

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"moyue/internal/comic"
	"moyue/internal/epub"
	"moyue/internal/fb2"
	"moyue/internal/kindle"
	"moyue/internal/store"
	"moyue/internal/txt"
	"moyue/internal/webdav"
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

type scanCounts struct {
	added    int
	updated  int
	skipped  int
	removed  int
	failed   int
	firstErr string
}

func (c *scanCounts) fail(err error) {
	c.failed++
	if c.firstErr == "" && err != nil {
		c.firstErr = err.Error()
	}
}

func (s *Server) runScan(libID string) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("书库扫描异常", "library", libID, "err", rec, "stack", string(debug.Stack()))
			s.finishScan(libID, 0, 0, 0, 0, 1, fmt.Sprintf("扫描异常：%v", rec))
		}
	}()
	lib, err := s.store.GetLibrary(libID)
	if err != nil {
		s.finishScan(libID, 0, 0, 0, 0, 1, "书库不存在")
		return
	}
	if lib.Kind == "webdav" {
		s.runWebDAVScan(lib)
		return
	}

	counts := &scanCounts{}
	seen := map[string]bool{}
	clean := true
	before := s.store.BookCount(lib.ID)
	root := lib.Path
	if resolved, err := filepath.EvalSymlinks(lib.Path); err == nil {
		root = resolved
	} else {
		counts.fail(fmt.Errorf("书库路径不可访问：%w", err))
		clean = false
	}
	if clean {
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				counts.fail(walkErr)
				clean = false
				return nil
			}
			if d.IsDir() {
				if p != root {
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
				counts.fail(err)
				clean = false
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil || strings.HasPrefix(rel, "..") {
				counts.fail(err)
				clean = false
				return nil
			}
			rel = filepath.ToSlash(rel)
			seen[store.BookID(lib.ID, rel)] = true
			s.processScannedFile(lib, rel, p, info, counts)
			return nil
		})
		if err != nil {
			counts.fail(err)
			clean = false
		}
	}
	if clean {
		// 空目录/未挂载的挂载点会让扫描结果为空。此时保留原有记录，
		// 避免网络存储短暂离线或被清空时连带删除所有用户的阅读数据。
		if len(seen) == 0 && before > 0 {
			counts.fail(fmt.Errorf("扫描结果为空，已保留原有 %d 条书籍记录，请检查书库挂载", before))
		} else if removed, err := s.store.PruneBooks(lib.ID, seen); err != nil {
			counts.fail(err)
		} else {
			counts.removed = removed
		}
	}
	s.finishScan(libID, counts.added, counts.updated, counts.skipped, counts.removed, counts.failed, counts.firstErr)
	slog.Info("书库扫描完成", "library", lib.Name, "kind", lib.Kind,
		"added", counts.added, "updated", counts.updated,
		"skipped", counts.skipped, "removed", counts.removed, "failed", counts.failed)
}

// runWebDAVScan 只登记远程文件与元数据；正文在首次打开时按需下载。
func (s *Server) runWebDAVScan(lib *store.Library) {
	counts := &scanCounts{}
	client, err := webdav.New(lib.RemoteURL, lib.RemoteUser, lib.RemotePass)
	if err != nil {
		s.finishScan(lib.ID, 0, 0, 0, 0, 1, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	files, err := client.List(ctx)
	if err != nil {
		s.finishScan(lib.ID, 0, 0, 0, 0, 1, "读取 WebDAV 目录失败："+err.Error())
		return
	}

	seen := map[string]bool{}
	before := s.store.BookCount(lib.ID)
	for _, rf := range files {
		if !supportedFile(lib.Type, rf.Path) {
			continue
		}
		rel := filepath.ToSlash(rf.Path)
		id := store.BookID(lib.ID, rel)
		seen[id] = true
		existing, _ := s.store.GetBook(id)
		same := existing != nil
		if same && rf.Size > 0 && existing.Size != rf.Size {
			same = false
		}
		if same && !rf.ModTime.IsZero() && existing.ModTime != rf.ModTime.UnixNano() {
			same = false
		}
		if same {
			counts.skipped++
			s.updateScanProgress(lib.ID, counts.added, counts.updated, counts.skipped, counts.failed)
			continue
		}
		// 远程内容变化后丢弃本地缓存，下次打开时重新下载与解析。
		local := filepath.Join(lib.Path, filepath.FromSlash(rel))
		if pathInside(lib.Path, local) {
			_ = os.Remove(local)
		}
		book := webdavBookStub(lib, rel, rf, existing)
		if err := s.store.AddBook(book); err != nil {
			counts.fail(err)
		} else {
			s.clearBookCache(book.ID)
			if existing != nil {
				counts.updated++
			} else {
				counts.added++
			}
		}
		s.updateScanProgress(lib.ID, counts.added, counts.updated, counts.skipped, counts.failed)
	}

	if len(seen) == 0 && before > 0 {
		counts.fail(fmt.Errorf("WebDAV 扫描结果为空，已保留原有 %d 条书籍记录与缓存", before))
	} else {
		removed, err := s.store.PruneBooks(lib.ID, seen)
		if err != nil {
			counts.fail(err)
		} else {
			counts.removed = removed
		}
		s.pruneWebDAVCache(lib, seen)
	}
	s.finishScan(lib.ID, counts.added, counts.updated, counts.skipped, counts.removed, counts.failed, counts.firstErr)
	slog.Info("WebDAV 书库扫描完成", "library", lib.Name,
		"added", counts.added, "updated", counts.updated,
		"skipped", counts.skipped, "removed", counts.removed, "failed", counts.failed)
}

// pruneWebDAVCache 删除远端已不存在的书对应的本地缓存文件。
func (s *Server) pruneWebDAVCache(lib *store.Library, seen map[string]bool) {
	if _, err := os.Stat(lib.Path); err != nil {
		return
	}
	_ = filepath.WalkDir(lib.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(lib.Path, p)
		if err != nil {
			return nil
		}
		if !seen[store.BookID(lib.ID, filepath.ToSlash(rel))] {
			_ = os.Remove(p)
		}
		return nil
	})
}

func webdavBookStub(lib *store.Library, rel string, rf webdav.File, existing *store.Book) *store.Book {
	title, creator := mediaName(rel)
	b := &store.Book{
		ID:          store.BookID(lib.ID, rel),
		LibraryID:   lib.ID,
		Type:        bookType(rel),
		File:        rel,
		Title:       title,
		Creator:     creator,
		Language:    "zh-CN",
		Size:        rf.Size,
		ModTime:     rf.ModTime.UnixNano(),
		Description: "WebDAV · 打开时下载",
		AddedAt:     time.Now(),
	}
	if existing != nil {
		b.AddedAt = existing.AddedAt
	}
	return b
}

func bookType(rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".epub":
		return "epub"
	case ".txt":
		return "txt"
	case ".pdf":
		return "pdf"
	case ".fb2":
		return "fb2"
	case ".cbz", ".cbr", ".mobi", ".azw", ".azw3", ".prc":
		return strings.TrimPrefix(ext, ".")
	case ".mp3", ".m4a", ".m4b", ".flac", ".ogg", ".opus", ".wav":
		return "audio"
	}
	return ""
}

func (s *Server) processScannedFile(lib *store.Library, rel, full string, info os.FileInfo, counts *scanCounts) {
	defer func() {
		if rec := recover(); rec != nil {
			counts.fail(fmt.Errorf("%s: 解析异常: %v", rel, rec))
			slog.Error("解析书籍异常", "file", full, "err", rec, "stack", string(debug.Stack()))
		}
	}()
	id := store.BookID(lib.ID, rel)
	existing, _ := s.store.GetBook(id)
	if existing != nil && existing.Size == info.Size() && existing.ModTime == info.ModTime().UnixNano() {
		counts.skipped++
		s.updateScanProgress(lib.ID, counts.added, counts.updated, counts.skipped, counts.failed)
		return
	}

	book, err := s.buildBook(lib, rel, full, info)
	if err != nil {
		counts.fail(fmt.Errorf("%s: %w", rel, err))
		slog.Warn("解析书籍失败", "file", full, "err", err)
		s.updateScanProgress(lib.ID, counts.added, counts.updated, counts.skipped, counts.failed)
		return
	}
	if existing != nil {
		book.AddedAt = existing.AddedAt
		counts.updated++
	} else {
		counts.added++
	}
	if err := s.store.AddBook(book); err != nil {
		counts.fail(err)
	} else {
		s.clearBookCache(book.ID)
	}
	s.updateScanProgress(lib.ID, counts.added, counts.updated, counts.skipped, counts.failed)
}

func pathInside(root, target string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	t, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, t)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) updateScanProgress(id string, added, updated, skipped, failed int) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if st, ok := s.scanStatus[id]; ok {
		st.Added, st.Updated, st.Skipped, st.Failed = added, updated, skipped, failed
	}
}

func (s *Server) finishScan(id string, added, updated, skipped, removed, failed int, errMsg string) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	st, ok := s.scanStatus[id]
	if !ok {
		st = &ScanStatus{}
		s.scanStatus[id] = st
	}
	now := time.Now()
	st.Running = false
	st.Added, st.Updated, st.Skipped, st.Removed, st.Failed = added, updated, skipped, removed, failed
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
		if info.Size() > maxKindleBookBytes {
			return nil, fmt.Errorf("Kindle 文件超过 %d MB 上限", maxKindleBookBytes>>20)
		}
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
