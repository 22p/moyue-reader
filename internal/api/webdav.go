package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"moyue/internal/store"
	"moyue/internal/webdav"
)

// ensureBookContent 在首次打开 WebDAV 书籍时按需下载并解析。
// 本地书库直接返回原记录。
func (s *Server) ensureBookContent(b *store.Book) (*store.Book, error) {
	if b == nil {
		return nil, fmt.Errorf("书籍不存在")
	}
	lib, err := s.store.GetLibrary(b.LibraryID)
	if err != nil {
		return nil, err
	}
	if lib.Kind != "webdav" {
		return b, nil
	}

	s.webdavMu.Lock()
	defer s.webdavMu.Unlock()

	// 等锁期间可能已被另一个请求下载并解析完成。
	if latest, err := s.store.GetBook(b.ID); err == nil {
		b = latest
	}

	local := filepath.Join(lib.Path, filepath.FromSlash(b.File))
	if !pathInside(lib.Path, local) {
		return nil, fmt.Errorf("书籍路径超出缓存目录")
	}
	info, statErr := os.Stat(local)
	needDownload := statErr != nil
	if !needDownload && b.Size > 0 && info.Size() != b.Size {
		needDownload = true
	}
	needParse := b.ChapterCount == 0
	if !needDownload && !needParse {
		return b, nil
	}

	if needDownload {
		client, err := webdav.New(lib.RemoteURL, lib.RemoteUser, lib.RemotePass)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := client.Download(ctx, b.File, local); err != nil {
			return nil, err
		}
		if b.ModTime > 0 {
			mt := time.Unix(0, b.ModTime)
			_ = os.Chtimes(local, mt, mt)
		}
		info, err = os.Stat(local)
		if err != nil {
			return nil, err
		}
	}

	parsed, err := s.buildBook(lib, b.File, local, info)
	if err != nil {
		_ = os.Remove(local)
		return nil, err
	}
	parsed.AddedAt = b.AddedAt
	if err := s.store.AddBook(parsed); err != nil {
		return nil, err
	}
	s.clearBookCache(parsed.ID)
	return parsed, nil
}
