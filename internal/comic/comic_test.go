package comic

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func makeCBZ(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.cbz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"p10.jpg", "p2.jpg", "p1.jpg", "cover.jpg", "notes.txt", "__MACOSX/p3.jpg"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("data-" + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestListNaturalOrder(t *testing.T) {
	path := makeCBZ(t)
	pages, err := List(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cover.jpg", "p1.jpg", "p2.jpg", "p10.jpg"}
	if len(pages) != len(want) {
		t.Fatalf("pages = %d, want %d: %+v", len(pages), len(want), pages)
	}
	for i := range want {
		if pages[i].Name != want[i] {
			t.Errorf("page %d = %q, want %q", i, pages[i].Name, want[i])
		}
	}
}

func TestReadPage(t *testing.T) {
	path := makeCBZ(t)
	data, err := Read(path, "p2.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "data-p2.jpg" {
		t.Errorf("data = %q", data)
	}
	if _, err := Read(path, "missing.jpg"); err == nil {
		t.Error("missing page should error")
	}
}

func TestNaturalLess(t *testing.T) {
	cases := [][2]string{
		{"p2.jpg", "p10.jpg"},
		{"a/01.jpg", "a/2.jpg"},
	}
	for _, c := range cases {
		if !naturalLess(c[0], c[1]) {
			t.Errorf("%q should sort before %q", c[0], c[1])
		}
	}
}
