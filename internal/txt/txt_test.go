package txt

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestSplitChineseChapters(t *testing.T) {
	text := strings.TrimSpace(`《山海小记》

作者：沈青

序章

山在海的尽头，海在山的外面。

第一章 出发

天还没亮，我就出了门。

第二章 渡口

渡口的船夫姓周。

第二卷 归途

第三章 夜路

月亮升起来了。

第四章 到家

院门虚掩着。`)

	book, err := Parse([]byte(text), "山海小记 - 沈青.txt")
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "山海小记" {
		t.Errorf("title = %q", book.Title)
	}
	if book.Author != "沈青" {
		t.Errorf("author = %q", book.Author)
	}
	if len(book.Chapters) != 5 {
		for _, c := range book.Chapters {
			t.Logf("chapter: %q (%d-%d)", c.Title, c.Start, c.End)
		}
		t.Fatalf("chapters = %d, want 5", len(book.Chapters))
	}
	if book.Chapters[0].Title != "序章" {
		t.Errorf("first = %q", book.Chapters[0].Title)
	}
	if book.Chapters[1].Title != "第一章 出发" {
		t.Errorf("second = %q", book.Chapters[1].Title)
	}
	if !strings.Contains(book.Text[book.Chapters[1].Start:book.Chapters[1].End], "天还没亮") {
		t.Error("chapter range wrong")
	}
}

func TestSplitInlineTitle(t *testing.T) {
	text := "第一章初见\n\n雨下了一夜。\n\n第二章离别\n\n她走了。"
	chapters := Split(text)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}
	if chapters[0].Title != "第一章初见" {
		t.Errorf("title = %q", chapters[0].Title)
	}
}

func TestSplitVolumeMerged(t *testing.T) {
	text := "第一卷 少年游\n第一章 山门\n\n他挑着水上山。\n\n第二章 风雪\n\n雪封了路。"
	chapters := Split(text)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2: %+v", len(chapters), chapters)
	}
	if chapters[0].Title != "第一章 山门" {
		t.Errorf("title = %q", chapters[0].Title)
	}
}

func TestSplitFallbackChunk(t *testing.T) {
	text := strings.Repeat("这是一段没有任何章节标题的长文本。\n\n", 2000)
	chapters := Split(text)
	if len(chapters) < 2 {
		t.Fatalf("expected chunked chapters, got %d", len(chapters))
	}
	if chapters[0].Title != "正文" {
		t.Errorf("first title = %q", chapters[0].Title)
	}
}

func TestGB18030Decode(t *testing.T) {
	text := "第一章 山海\n\n山在那一边。"
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	book, err := Parse(encoded, "测试.txt")
	if err != nil {
		t.Fatal(err)
	}
	if book.Encoding != "gb18030" {
		t.Errorf("encoding = %q", book.Encoding)
	}
	if !strings.Contains(book.Text, "山在那一边") {
		t.Error("gb18030 decode failed")
	}
}

func TestHTMLRenderEscapes(t *testing.T) {
	out := HTML("第一段 & <b>标签</b>\n仍然第一段\n\n***\n\n第二段")
	if !strings.Contains(out, "&amp;") || strings.Contains(out, "<b>") {
		t.Errorf("escaping failed: %s", out)
	}
	if !strings.Contains(out, "txt-sep") {
		t.Error("separator not rendered")
	}
	if strings.Count(out, "<p") != 3 {
		t.Errorf("paragraph count wrong: %s", out)
	}
}

func TestIsHeadingRejectsParagraph(t *testing.T) {
	cases := []string{
		"第一章的内容非常精彩，我读了三遍。",
		"他说：“第二章还没写完。”",
		"今天我去了书店。",
	}
	for _, c := range cases {
		if IsHeading(c) {
			t.Errorf("should not be heading: %q", c)
		}
	}
	if !IsHeading("第12章 雪落长安") {
		t.Error("valid heading rejected")
	}
	if !IsHeading("番外 后日谈") {
		t.Error("special heading rejected")
	}
}
