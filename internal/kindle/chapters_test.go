package kindle

import (
	"strings"
	"testing"
)

func TestSplitChapters(t *testing.T) {
	b := &Book{HTML: `<?xml version="1.0"?><html><head><style>p{}</style></head><body>
<h1>第一章</h1><p>正文一<img recindex="00001"></p>
<h1>第二章</h1><p>正文二</p></body></html>`}
	chapters := b.SplitChapters("book1")
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d", len(chapters))
	}
	if chapters[0].Title != "第一章" || chapters[1].Title != "第二章" {
		t.Errorf("titles = %q %q", chapters[0].Title, chapters[1].Title)
	}
	if strings.Contains(chapters[0].HTML, "<style>") {
		t.Errorf("markup not normalized: %s", chapters[0].HTML)
	}
	if !strings.Contains(chapters[0].HTML, "/api/books/book1/kindleimage/1") {
		t.Errorf("image not rewritten: %s", chapters[0].HTML)
	}
}

func TestSplitByPageBreak(t *testing.T) {
	b := &Book{HTML: `<html><body><p>前言</p><mbp:pagebreak><p>第二部分</p><mbp:pagebreak/><p>第三部分</p></body></html>`}
	chapters := b.SplitChapters("b")
	if len(chapters) != 3 {
		t.Fatalf("chapters = %d", len(chapters))
	}
	if !strings.Contains(chapters[0].HTML, "前言") || !strings.Contains(chapters[2].HTML, "第三部分") {
		t.Errorf("split wrong: %+v", chapters)
	}
}
