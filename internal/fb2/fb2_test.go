package fb2

import (
	"encoding/base64"
	"strings"
	"testing"
)

var sample = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description><title-info>
<book-title>测试之书</book-title><lang>zh-CN</lang>
<author><first-name>张</first-name><last-name>三</last-name></author>
<annotation>一段简介</annotation>
<coverpage><image l:href="#cover.jpg"/></coverpage>
</title-info><publish-info><publisher>测试社</publisher><year>2026</year></publish-info></description>
<body>
<section><title><p>第一章 开始</p></title><p>正文<emphasis>强调</emphasis>结束。</p><image l:href="#pic.png"/></section>
<section><title><p>第二章 继续</p></title><p>更多内容。</p></section>
</body>
<binary id="cover.jpg" content-type="image/jpeg">` + base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0}) + `</binary>
<binary id="pic.png" content-type="image/png">` + base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'}) + `</binary>
</FictionBook>`

func TestParse(t *testing.T) {
	b, err := Parse([]byte(sample), "book123")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "测试之书" || b.Author != "张三" || b.Language != "zh-CN" {
		t.Errorf("metadata = %+v", b)
	}
	if b.Cover != "cover.jpg" || len(b.Images["cover.jpg"]) == 0 || len(b.Images["pic.png"]) == 0 {
		t.Errorf("images = %v, cover = %q", b.Images, b.Cover)
	}
	if len(b.Chapters) != 2 {
		t.Fatalf("chapters = %d", len(b.Chapters))
	}
	if b.Chapters[0].Title != "第一章 开始" || b.Chapters[1].Title != "第二章 继续" {
		t.Errorf("titles = %q, %q", b.Chapters[0].Title, b.Chapters[1].Title)
	}
	if !strings.Contains(b.Chapters[0].HTML, "<em>强调</em>") {
		t.Errorf("inline formatting missing: %s", b.Chapters[0].HTML)
	}
	if !strings.Contains(b.Chapters[0].HTML, "/api/books/book123/fb2image?name=pic.png") {
		t.Errorf("image url missing: %s", b.Chapters[0].HTML)
	}
}
