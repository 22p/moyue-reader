package epub

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

type testEntry struct {
	name string
	body string
}

func writeEPUB(t *testing.T, entries []testEntry) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "test.epub")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	// mimetype 必须是第一个、且不压缩的条目
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mw.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

const containerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

func TestOpenEPUB3(t *testing.T) {
	opf := `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>测试之书</dc:title>
    <dc:creator>张三</dc:creator>
    <dc:creator>李四</dc:creator>
    <dc:language>zh-CN</dc:language>
    <dc:description>&lt;p&gt;一段&lt;b&gt;简介&lt;/b&gt;。&lt;/p&gt;</dc:description>
    <meta name="cover" content="cover-img"/>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="cover-img" href="images/cover.jpg" media-type="image/jpeg" properties="cover-image"/>
    <item id="c1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
    <itemref idref="c2"/>
  </spine>
</package>`

	nav := `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><h1>目录</h1><ol>
  <li><a href="text/ch1.xhtml">第一章</a></li>
  <li><a href="text/ch2.xhtml">第二章</a>
    <ol><li><a href="text/ch2.xhtml#part">第二章 · 小节</a></li></ol>
  </li>
</ol></nav></body></html>`

	ch := `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>hi</h1></body></html>`

	file := writeEPUB(t, []testEntry{
		{"META-INF/container.xml", containerXML},
		{"OEBPS/content.opf", opf},
		{"OEBPS/nav.xhtml", nav},
		{"OEBPS/text/ch1.xhtml", ch},
		{"OEBPS/text/ch2.xhtml", ch},
		{"OEBPS/images/cover.jpg", "jpeg"},
	})

	book, err := Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if book.Title != "测试之书" {
		t.Errorf("Title = %q", book.Title)
	}
	if book.Creator != "张三、李四" {
		t.Errorf("Creator = %q", book.Creator)
	}
	if book.Description != "一段简介。" {
		t.Errorf("Description = %q", book.Description)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("Chapters = %d, want 2", len(book.Chapters))
	}
	if book.Chapters[0].Href != "OEBPS/text/ch1.xhtml" {
		t.Errorf("chapter href = %q", book.Chapters[0].Href)
	}
	if book.Chapters[0].Title != "第一章" {
		t.Errorf("chapter title = %q", book.Chapters[0].Title)
	}
	if book.CoverHref != "OEBPS/images/cover.jpg" {
		t.Errorf("cover = %q", book.CoverHref)
	}
	if len(book.TOC) != 3 {
		t.Fatalf("TOC = %d, want 3", len(book.TOC))
	}
	if book.TOC[2].Level != 1 {
		t.Errorf("nested level = %d, want 1", book.TOC[2].Level)
	}
	if book.TOC[1].Chapter != 1 || book.TOC[2].Chapter != 1 {
		t.Errorf("toc chapter mapping wrong: %+v", book.TOC)
	}
}

func TestOpenEPUB2NCX(t *testing.T) {
	opf := `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>旧格式</dc:title><dc:language>zh-CN</dc:language>
  </metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="a" href="a.html" media-type="application/xhtml+xml"/>
    <item id="b" href="b.html" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx"><itemref idref="a"/><itemref idref="b"/></spine>
</package>`

	ncx := `<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint id="n1"><navLabel><text>开篇</text></navLabel><content src="a.html"/>
      <navPoint id="n2"><navLabel><text>开篇 · 注</text></navLabel><content src="a.html#note"/></navPoint>
    </navPoint>
    <navPoint id="n3"><navLabel><text>终章</text></navLabel><content src="b.html"/></navPoint>
  </navMap>
</ncx>`

	file := writeEPUB(t, []testEntry{
		{"META-INF/container.xml", containerXML},
		{"OEBPS/content.opf", opf},
		{"OEBPS/toc.ncx", ncx},
		{"OEBPS/a.html", "<html><body>A</body></html>"},
		{"OEBPS/b.html", "<html><body>B</body></html>"},
	})

	book, err := Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("Chapters = %d", len(book.Chapters))
	}
	if len(book.TOC) != 3 {
		t.Fatalf("TOC = %d, want 3", len(book.TOC))
	}
	if book.TOC[1].Level != 1 {
		t.Errorf("level = %d, want 1", book.TOC[1].Level)
	}
	if book.TOC[1].Chapter != 0 {
		t.Errorf("fragment toc should map to chapter 0, got %d", book.TOC[1].Chapter)
	}
	if book.TOC[2].Chapter != 1 {
		t.Errorf("chapter = %d, want 1", book.TOC[2].Chapter)
	}
	if book.CoverHref != "" {
		t.Errorf("unexpected cover %q", book.CoverHref)
	}
}

func TestOpenRejectsNonEPUB(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.epub")
	if err := os.WriteFile(file, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file); err == nil {
		t.Fatal("expected error for non-epub file")
	}
}
