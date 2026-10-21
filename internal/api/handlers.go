package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
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

type bookView struct {
	*store.Book
	Progress store.Progress `json:"progress"`
}

type chapterInfo struct {
	Index int    `json:"index"`
	Title string `json:"title"`
}

type tocEntry struct {
	Title   string `json:"title"`
	Level   int    `json:"level"`
	Chapter int    `json:"chapter"`
}

type bookDetail struct {
	*store.Book
	Progress    store.Progress     `json:"progress"`
	Bookmarks   []store.Bookmark   `json:"bookmarks"`
	Annotations []store.Annotation `json:"annotations"`
	Chapters    []chapterInfo      `json:"chapters"`
	TOC         []tocEntry         `json:"toc"`
}

/* ------------------------------- 书架 ------------------------------- */

func (s *Server) listBooks(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	filter := r.URL.Query().Get("library")
	if filter != "" && filter != "all" {
		if _, err := s.store.GetLibrary(filter); err != nil {
			writeError(w, http.StatusNotFound, "书库不存在")
			return
		}
		if !s.store.CanAccess(u, filter) {
			writeError(w, http.StatusForbidden, "没有该书的访问权限")
			return
		}
	}

	books := s.store.ListBooks()
	progress := s.store.ProgressMap(u.ID)
	out := make([]bookView, 0, len(books))
	for _, b := range books {
		if !s.store.CanAccess(u, b.LibraryID) {
			continue
		}
		if filter != "" && filter != "all" && b.LibraryID != filter {
			continue
		}
		out = append(out, bookView{Book: b, Progress: progress[b.ID]})
	}
	sort.Slice(out, func(i, j int) bool {
		return activityTime(out[i].Book) > activityTime(out[j].Book)
	})
	writeJSON(w, http.StatusOK, map[string]any{"books": out})
}

func activityTime(b *store.Book) int64 {
	return b.AddedAt.Unix()
}

func (s *Server) bookDetail(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	d := bookDetail{
		Book:        b,
		Progress:    s.store.ProgressFor(u.ID, b.ID),
		Bookmarks:   s.store.BookmarksFor(u.ID, b.ID),
		Annotations: s.store.ListAnnotations(u.ID, b.ID),
		Chapters:    []chapterInfo{},
		TOC:         []tocEntry{},
	}

	switch b.Type {
	case "txt":
		tb, err := s.parseTXT(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "TXT 解析失败："+err.Error())
			return
		}
		for i, ch := range tb.Chapters {
			title := ch.Title
			if title == "" {
				title = "第 " + strconv.Itoa(i+1) + " 节"
			}
			d.Chapters = append(d.Chapters, chapterInfo{Index: i, Title: title})
			d.TOC = append(d.TOC, tocEntry{Title: title, Level: 0, Chapter: i})
		}
	case "cbz", "cbr":
		pages, err := s.comicPages(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "漫画包解析失败："+err.Error())
			return
		}
		for i := range pages {
			title := "第 " + strconv.Itoa(i+1) + " 页"
			d.Chapters = append(d.Chapters, chapterInfo{Index: i, Title: title})
			d.TOC = append(d.TOC, tocEntry{Title: title, Level: 0, Chapter: i})
		}
	case "audio":
		d.Chapters = append(d.Chapters, chapterInfo{Index: 0, Title: "音频"})
		d.TOC = append(d.TOC, tocEntry{Title: "音频", Level: 0, Chapter: 0})
	case "fb2":
		fb, err := s.parseFB2(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "FB2 解析失败："+err.Error())
			return
		}
		for i, ch := range fb.Chapters {
			d.Chapters = append(d.Chapters, chapterInfo{Index: i, Title: ch.Title})
			d.TOC = append(d.TOC, tocEntry{Title: ch.Title, Level: 0, Chapter: i})
		}
	case "mobi", "azw", "azw3", "prc":
		_, chapters, err := s.parseKindle(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "MOBI/AZW3 解析失败："+err.Error())
			return
		}
		for i, ch := range chapters {
			d.Chapters = append(d.Chapters, chapterInfo{Index: i, Title: ch.Title})
			d.TOC = append(d.TOC, tocEntry{Title: ch.Title, Level: 0, Chapter: i})
		}
	case "pdf":
		if b.ChapterCount <= 0 {
			if n, err := pdfPageCount(s.bookPath(b)); err == nil {
				b.ChapterCount = n
				_ = s.store.AddBook(b)
			}
		}
		for i := 0; i < b.ChapterCount; i++ {
			title := "第 " + strconv.Itoa(i+1) + " 页"
			d.Chapters = append(d.Chapters, chapterInfo{Index: i, Title: title})
			d.TOC = append(d.TOC, tocEntry{Title: title, Level: 0, Chapter: i})
		}
	default:
		eb, err := s.parseEPUB(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "EPUB 解析失败："+err.Error())
			return
		}
		for i, ch := range eb.Chapters {
			d.Chapters = append(d.Chapters, chapterInfo{Index: i, Title: ch.Title})
		}
		for _, t := range eb.TOC {
			d.TOC = append(d.TOC, tocEntry{Title: t.Title, Level: t.Level, Chapter: t.Chapter})
		}
	}
	if len(d.Chapters) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "这本书没有可阅读的内容")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

/* ------------------------------- 上传 ------------------------------- */

const (
	maxUploadBytes     = 200 << 20
	maxKindleBookBytes = 128 << 20
	maxRemoteBookBytes = 512 << 20
)

func (s *Server) uploadBook(w http.ResponseWriter, r *http.Request) {
	extendReadDeadline(w, 30*time.Minute)
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "上传内容过大或格式不正确")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "文件超过 200MB 上限")
		return
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !uploadableExt(ext) {
		writeError(w, http.StatusBadRequest, "支持 EPUB、TXT、PDF、CBZ、CBR、FB2、MOBI/AZW3 与音频文件")
		return
	}
	lib, err := s.uploadTarget(r.FormValue("libraryId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(lib.Path, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "书库目录不可写")
		return
	}

	tmp, err := os.CreateTemp(lib.Path, ".upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法写入书库目录")
		return
	}
	tmpName := tmp.Name()
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		writeError(w, http.StatusBadRequest, "读取上传文件失败")
		return
	}
	if n > maxUploadBytes {
		tmp.Close()
		os.Remove(tmpName)
		writeError(w, http.StatusRequestEntityTooLarge, "文件超过 200MB 上限")
		return
	}
	tmp.Close()
	checksum := hex.EncodeToString(hasher.Sum(nil))

	if existing, ok := s.store.FindBookByChecksum(lib.ID, checksum); ok {
		os.Remove(tmpName)
		writeJSON(w, http.StatusOK, map[string]any{
			"book":       bookView{Book: existing, Progress: s.store.ProgressFor(currentUser(r).ID, existing.ID)},
			"duplicated": true,
		})
		return
	}

	base := sanitizeFilename(strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename)))
	if base == "" {
		base = "未命名"
	}
	name := base + ext
	for i := 2; fileExists(filepath.Join(lib.Path, name)); i++ {
		name = base + " (" + strconv.Itoa(i) + ")" + ext
	}
	final := filepath.Join(lib.Path, name)
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "保存文件失败")
		return
	}

	info, err := os.Stat(final)
	if err != nil {
		os.Remove(final)
		writeError(w, http.StatusInternalServerError, "读取文件失败")
		return
	}
	rec, err := s.buildBook(lib, filepath.ToSlash(name), final, info)
	if err != nil {
		os.Remove(final)
		writeError(w, http.StatusBadRequest, "无法解析该文件："+err.Error())
		return
	}
	rec.Checksum = checksum
	if old, _ := s.store.GetBook(rec.ID); old != nil {
		rec.AddedAt = old.AddedAt
	}
	if err := s.store.AddBook(rec); err != nil {
		os.Remove(final)
		writeError(w, http.StatusInternalServerError, "保存书籍信息失败")
		return
	}
	s.clearBookCache(rec.ID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"book":       bookView{Book: rec, Progress: store.Progress{}},
		"duplicated": false,
	})
}

func (s *Server) uploadTarget(libID string) (*store.Library, error) {
	if libID != "" {
		lib, err := s.store.GetLibrary(libID)
		if err != nil {
			return nil, errors.New("目标书库不存在")
		}
		if lib.Kind == "webdav" {
			return nil, errors.New("WebDAV 书库请在远端添加文件后重新扫描")
		}
		return lib, nil
	}
	libs := s.store.ListLibraries()
	abs, _ := filepath.Abs(s.booksDir)
	for _, l := range libs {
		if l.Kind != "webdav" && filepath.Clean(l.Path) == filepath.Clean(abs) {
			return l, nil
		}
	}
	for _, l := range libs {
		if l.Kind != "webdav" {
			return l, nil
		}
	}
	return nil, errors.New("没有可上传的本地书库，请先在管理控制台创建本地目录")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (s *Server) deleteBook(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.GetBook(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "书籍不存在")
		return
	}
	if err := s.store.DeleteBook(b.ID); err != nil {
		writeError(w, errStatus(err), "删除书籍失败")
		return
	}
	if p := s.bookPath(b); p != "" {
		_ = os.Remove(p)
	}
	s.clearBookCache(b.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func downloadExt(b *store.Book) string {
	if ext := strings.ToLower(filepath.Ext(b.File)); ext != "" {
		switch ext {
		case ".epub", ".txt", ".pdf", ".cbz", ".cbr", ".fb2", ".mobi",
			".azw", ".azw3", ".prc", ".mp3", ".m4a", ".m4b", ".flac",
			".ogg", ".opus", ".wav":
			return ext
		}
	}
	switch b.Type {
	case "txt":
		return ".txt"
	case "pdf":
		return ".pdf"
	case "cbz":
		return ".cbz"
	case "cbr":
		return ".cbr"
	case "fb2":
		return ".fb2"
	case "mobi", "azw", "azw3", "prc":
		return "." + b.Type
	case "audio":
		return ".mp3"
	default:
		return ".epub"
	}
}

func (s *Server) downloadBook(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, 30*time.Minute)
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	name := sanitizeFilename(b.Title)
	if name == "" {
		name = b.ID
	}
	ext := downloadExt(b)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ext}))
	http.ServeFile(w, r, s.bookPath(b))
}

/* ------------------------------- 封面 / 资源 ------------------------------- */

func (s *Server) bookCover(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type == "fb2" {
		fb, err := s.parseFB2(b)
		if err != nil || fb.Cover == "" {
			http.NotFound(w, r)
			return
		}
		data := fb.Images[fb.Cover]
		if len(data) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", fb.ImageTypes[fb.Cover])
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(data)
		return
	}
	if b.Type == "mobi" || b.Type == "azw" || b.Type == "azw3" || b.Type == "prc" {
		kb, _, err := s.parseKindle(b)
		if err != nil || kb.Cover == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", kindleMime(kb.Cover.Kind))
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(kb.Cover.Data)
		return
	}
	if b.Type == "cbz" || b.Type == "cbr" {
		pages, err := s.comicPages(b)
		if err != nil || len(pages) == 0 {
			http.NotFound(w, r)
			return
		}
		data, err := comic.Read(s.bookPath(b), pages[0].Name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType(pages[0].Name))
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(data)
		return
	}
	if b.Type != "epub" {
		http.NotFound(w, r)
		return
	}
	parsed, err := s.parseEPUB(b)
	if err != nil || parsed.CoverHref == "" {
		http.NotFound(w, r)
		return
	}
	ar, err := epub.OpenArchive(s.bookPath(b))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer ar.Close()
	data, err := ar.Read(parsed.CoverHref)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(parsed.CoverHref))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

func (s *Server) resource(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if b.Type != "epub" {
		http.NotFound(w, r)
		return
	}
	name := cleanPath(r.URL.Query().Get("path"))
	if name == "" || strings.Contains(name, "..") {
		writeError(w, http.StatusBadRequest, "资源路径无效")
		return
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".mjs") {
		writeError(w, http.StatusForbidden, "不允许访问脚本资源")
		return
	}

	ar, err := epub.OpenArchive(s.bookPath(b))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法打开电子书")
		return
	}
	defer ar.Close()
	data, err := ar.Read(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(lower, ".css") {
		dir := path.Dir(name)
		if dir == "." {
			dir = ""
		}
		ctx := &renderCtx{bookID: b.ID, spine: map[string]int{}, dir: dir}
		data = []byte(rewriteCSS(string(data), ctx))
	}
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

/* ------------------------------- 章节 ------------------------------- */

func (s *Server) chapter(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || idx < 0 {
		writeError(w, http.StatusBadRequest, "章节序号无效")
		return
	}

	var doc string
	switch b.Type {
	case "txt":
		tb, err := s.parseTXT(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "TXT 解析失败")
			return
		}
		if idx >= len(tb.Chapters) {
			writeError(w, http.StatusNotFound, "章节不存在")
			return
		}
		ch := tb.Chapters[idx]
		body := tb.Text[ch.Start:ch.End]
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			if txt.IsHeading(strings.TrimSpace(body[:i])) {
				body = body[i+1:]
			}
		}
		doc = renderTXTChapter(ch.Title, txt.HTML(body), b.Language)
	case "cbz", "cbr":
		pages, err := s.comicPages(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "漫画包解析失败")
			return
		}
		if idx >= len(pages) {
			writeError(w, http.StatusNotFound, "页面不存在")
			return
		}
		spread := r.URL.Query().Get("spread") == "1" && idx+1 < len(pages)
		doc = renderComicChapter(b.ID, idx, "第 "+strconv.Itoa(idx+1)+" 页", spread)
	case "pdf":
		writeError(w, http.StatusBadRequest, "PDF 请使用内置 PDF 阅读器")
		return
	case "audio":
		writeError(w, http.StatusBadRequest, "有声书请使用音频播放器")
		return
	case "fb2":
		fb, err := s.parseFB2(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "FB2 解析失败")
			return
		}
		if idx >= len(fb.Chapters) {
			writeError(w, http.StatusNotFound, "章节不存在")
			return
		}
		ch := fb.Chapters[idx]
		doc = renderChapter(b.ID, idx, "", []byte(ch.HTML), map[string]int{}, ch.Title, fb.Language)
	case "mobi", "azw", "azw3", "prc":
		_, chapters, err := s.parseKindle(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "MOBI/AZW3 解析失败")
			return
		}
		if idx >= len(chapters) {
			writeError(w, http.StatusNotFound, "章节不存在")
			return
		}
		ch := chapters[idx]
		doc = renderChapter(b.ID, idx, "", []byte(ch.HTML), map[string]int{}, ch.Title, b.Language)
	default:
		eb, err := s.parseEPUB(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "EPUB 解析失败")
			return
		}
		if idx >= len(eb.Chapters) {
			writeError(w, http.StatusNotFound, "章节不存在")
			return
		}
		ch := eb.Chapters[idx]
		ar, err := epub.OpenArchive(s.bookPath(b))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "无法打开电子书")
			return
		}
		defer ar.Close()
		raw, err := ar.Read(ch.Href)
		if err != nil {
			writeError(w, http.StatusNotFound, "章节内容不存在")
			return
		}
		spine := make(map[string]int, len(eb.Chapters))
		for i, c := range eb.Chapters {
			spine[strings.ToLower(c.Href)] = i
		}
		doc = renderChapter(b.ID, idx, ch.Href, raw, spine, ch.Title, eb.Language)
	}

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src * data: blob:; style-src * 'unsafe-inline'; font-src * data:; media-src *; script-src 'none'; object-src 'none'; form-action 'none'; base-uri 'none'")
	_, _ = w.Write([]byte(doc))
}

/* ------------------------------- 阅读数据 ------------------------------- */

func (s *Server) saveProgress(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	var req struct {
		Chapter        int     `json:"chapter"`
		ChapterPercent float64 `json:"chapterPercent"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	progress, err := s.store.SaveProgress(u.ID, b.ID, req.Chapter, req.ChapterPercent)
	if err != nil {
		writeError(w, errStatus(err), "保存进度失败")
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func (s *Server) resetProgress(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if err := s.store.ResetProgress(u.ID, b.ID); err != nil {
		writeError(w, errStatus(err), "重置进度失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) addReading(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	var req struct {
		Seconds int `json:"seconds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	if err := s.store.AddReading(u.ID, b.ID, req.Seconds); err != nil {
		writeError(w, errStatus(err), "记录阅读时长失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) addBookmark(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	var req struct {
		Chapter      int     `json:"chapter"`
		ChapterTitle string  `json:"chapterTitle"`
		Percent      float64 `json:"percent"`
		Snippet      string  `json:"snippet"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	req.ChapterTitle = truncate(req.ChapterTitle, 80)
	req.Snippet = truncate(req.Snippet, 160)
	bm, err := s.store.AddBookmark(u.ID, b.ID, store.Bookmark{
		Chapter:      req.Chapter,
		ChapterTitle: req.ChapterTitle,
		Percent:      clamp01(req.Percent),
		Snippet:      req.Snippet,
	})
	if err != nil {
		writeError(w, errStatus(err), "添加书签失败")
		return
	}
	writeJSON(w, http.StatusCreated, bm)
}

func (s *Server) deleteBookmark(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteBookmark(u.ID, b.ID, r.PathValue("bookmarkID")); err != nil {
		writeError(w, errStatus(err), "书签不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Stats(currentUser(r).ID))
}

/* ------------------------------- 解析缓存 ------------------------------- */

func (s *Server) loadBook(w http.ResponseWriter, r *http.Request) (*store.Book, *store.User, bool) {
	id := r.PathValue("id")
	b, err := s.store.GetBook(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "书籍不存在")
		return nil, nil, false
	}
	u := currentUser(r)
	if !s.canRead(u, b) {
		writeError(w, http.StatusForbidden, "没有这本书的访问权限")
		return nil, nil, false
	}
	b, err = s.ensureBookContent(r.Context(), b)
	if err != nil {
		writeError(w, http.StatusBadGateway, "加载书籍内容失败："+err.Error())
		return nil, nil, false
	}
	return b, u, true
}

func (s *Server) parseEPUB(b *store.Book) (*epub.Book, error) {
	file := s.bookPath(b)
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	cached, ok := s.epubCache[b.ID]
	s.mu.RUnlock()
	if ok && cached.modTime == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.book, nil
	}
	book, err := epub.Open(file)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.epubCache) > 12 {
		s.epubCache = map[string]cachedEPUB{}
	}
	s.epubCache[b.ID] = cachedEPUB{book: book, modTime: info.ModTime().UnixNano(), size: info.Size()}
	s.mu.Unlock()
	return book, nil
}

func (s *Server) parseTXT(b *store.Book) (*txt.Book, error) {
	file := s.bookPath(b)
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	cached, ok := s.txtCache[b.ID]
	s.mu.RUnlock()
	if ok && cached.modTime == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.book, nil
	}
	book, err := txt.ParseFile(file)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.txtCache) > 8 {
		s.txtCache = map[string]cachedTXT{}
	}
	s.txtCache[b.ID] = cachedTXT{book: book, modTime: info.ModTime().UnixNano(), size: info.Size()}
	s.mu.Unlock()
	return book, nil
}

func (s *Server) parseFB2(b *store.Book) (*fb2.Book, error) {
	file := s.bookPath(b)
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	cached, ok := s.fb2Cache[b.ID]
	s.mu.RUnlock()
	if ok && cached.modTime == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.book, nil
	}
	book, err := fb2.Open(file, b.ID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.fb2Cache) > 8 {
		s.fb2Cache = map[string]cachedFB2{}
	}
	s.fb2Cache[b.ID] = cachedFB2{book: book, modTime: info.ModTime().UnixNano(), size: info.Size()}
	s.mu.Unlock()
	return book, nil
}

func (s *Server) parseKindle(b *store.Book) (*kindle.Book, []kindle.Chapter, error) {
	file := s.bookPath(b)
	info, err := os.Stat(file)
	if err != nil {
		return nil, nil, err
	}
	if info.Size() > maxKindleBookBytes {
		return nil, nil, fmt.Errorf("Kindle 文件超过 %d MB 上限", maxKindleBookBytes>>20)
	}
	s.mu.RLock()
	cached, ok := s.kindleCache[b.ID]
	s.mu.RUnlock()
	if ok && cached.modTime == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.book, cached.chapters, nil
	}
	book, err := kindle.Open(file)
	if err != nil {
		return nil, nil, err
	}
	chapters := book.SplitChapters(b.ID)
	if len(chapters) == 0 {
		return nil, nil, errors.New("书中没有可阅读的正文")
	}
	s.mu.Lock()
	if len(s.kindleCache) > 6 {
		s.kindleCache = map[string]cachedKindle{}
	}
	s.kindleCache[b.ID] = cachedKindle{book: book, chapters: chapters, modTime: info.ModTime().UnixNano(), size: info.Size()}
	s.mu.Unlock()
	return book, chapters, nil
}

/* ------------------------------- 工具 ------------------------------- */

func uploadableExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".epub", ".txt", ".pdf", ".cbz", ".cbr", ".fb2", ".mobi", ".azw", ".azw3", ".prc":
		return true
	case ".mp3", ".m4a", ".m4b", ".flac", ".ogg", ".opus", ".wav":
		return true
	default:
		return false
	}
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			return '-'
		}
		return r
	}, s)
	s = strings.Trim(s, ". ")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}

func truncate(s string, limit int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return string(r)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".svg":
		return "image/svg+xml"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".m4b":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".mp4":
		return "video/mp4"
	case ".bmp":
		return "image/bmp"
	case ".xhtml", ".html", ".htm":
		return "application/xhtml+xml; charset=utf-8"
	case ".xml", ".ncx", ".opf":
		return "application/xml; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
