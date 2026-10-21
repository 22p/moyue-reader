package api

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"moyue/internal/store"
)

func newScanTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	web := fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<!doctype html>")},
		"pdfview.html":         &fstest.MapFile{Data: []byte("<!doctype html>")},
		"audioview.html":       &fstest.MapFile{Data: []byte("<!doctype html>")},
		"manifest.webmanifest": &fstest.MapFile{Data: []byte("{}")},
		"sw.js":                &fstest.MapFile{Data: []byte("")},
		"offline.html":         &fstest.MapFile{Data: []byte("<!doctype html>")},
	}
	srv, err := New(st, filepath.Join(root, "books"), web)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
}

func TestEmptyScanKeepsExistingBooks(t *testing.T) {
	srv, st := newScanTestServer(t)
	libDir := t.TempDir()
	lib := &store.Library{Name: "临时离线库", Path: libDir, Type: "mixed"}
	if err := st.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	book := &store.Book{LibraryID: lib.ID, Type: "txt", File: "removed.txt", Title: "离线期间保留"}
	if err := st.AddBook(book); err != nil {
		t.Fatal(err)
	}

	srv.runScan(lib.ID)

	if _, err := st.GetBook(book.ID); err != nil {
		t.Fatalf("empty scan removed book record: %v", err)
	}
	if status := srv.scanSnapshot(lib.ID); status.Failed == 0 {
		t.Error("empty scan should report a warning/error")
	}
}

func TestScanFollowsLibraryRootSymlink(t *testing.T) {
	srv, st := newScanTestServer(t)
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "book.txt"), []byte("第一章 开始\n\n正文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "books-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	lib := &store.Library{Name: "软链接库", Path: link, Type: "mixed"}
	if err := st.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}

	srv.runScan(lib.ID)

	if got := st.BookCount(lib.ID); got != 1 {
		t.Fatalf("books under symlink root = %d, want 1", got)
	}
	if status := srv.scanSnapshot(lib.ID); status.Failed != 0 {
		t.Fatalf("scan failed: %+v", status)
	}
}
