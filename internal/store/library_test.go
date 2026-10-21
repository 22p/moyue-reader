package store

import "testing"

func TestWebDAVLibraryRoundTrip(t *testing.T) {
	s := newTestStore(t)
	lib := &Library{
		Name:       "远程书库",
		Path:       t.TempDir(),
		Type:       "mixed",
		Kind:       "webdav",
		RemoteURL:  "https://example.com/dav/books",
		RemoteUser: "reader",
		RemotePass: "secret",
	}
	if err := s.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetLibrary(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "webdav" || got.RemoteURL != lib.RemoteURL ||
		got.RemoteUser != "reader" || got.RemotePass != "secret" {
		t.Fatalf("got %+v", got)
	}
	if got.Path != lib.Path {
		t.Fatalf("path = %q, want %q", got.Path, lib.Path)
	}
}

func TestPruneBooks(t *testing.T) {
	s := newTestStore(t)
	lib := &Library{Name: "书库", Path: t.TempDir(), Type: "mixed"}
	if err := s.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.epub", "b.txt"} {
		if err := s.AddBook(&Book{LibraryID: lib.ID, Type: "epub", File: name, Title: name}); err != nil {
			t.Fatal(err)
		}
	}
	keep := map[string]bool{BookID(lib.ID, "a.epub"): true}
	n, err := s.PruneBooks(lib.ID, keep)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed = %d", n)
	}
	books := s.ListBooks()
	if len(books) != 1 || books[0].File != "a.epub" {
		t.Fatalf("books = %+v", books)
	}
}
