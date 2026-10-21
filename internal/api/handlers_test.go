package api

import (
	"testing"

	"moyue/internal/store"
)

func TestDownloadExtUsesBookExtension(t *testing.T) {
	cases := []struct {
		book *store.Book
		want string
	}{
		{&store.Book{Type: "epub", File: "a.epub"}, ".epub"},
		{&store.Book{Type: "fb2", File: "a.fb2"}, ".fb2"},
		{&store.Book{Type: "azw3", File: "a.azw3"}, ".azw3"},
		{&store.Book{Type: "mobi", File: "a.mobi"}, ".mobi"},
		{&store.Book{Type: "audio", File: "a.m4a"}, ".m4a"},
		{&store.Book{Type: "audio", File: "a.mp3"}, ".mp3"},
		{&store.Book{Type: "txt", File: "a.txt"}, ".txt"},
	}
	for _, c := range cases {
		if got := downloadExt(c.book); got != c.want {
			t.Errorf("downloadExt(%+v) = %q, want %q", c.book, got, c.want)
		}
	}
}
