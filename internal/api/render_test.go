package api

import (
	"strings"
	"testing"
)

func TestRenderChapterRewritesAndSanitizes(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title>第二章</title>
  <link rel="stylesheet" type="text/css" href="../styles/book.css"/>
  <script>alert('xss')</script>
</head>
<body onload="evil()">
  <h1 onclick="evil()">第二章</h1>
  <p>正文 <a href="ch03.xhtml#next">下一章</a>，<a href="https://example.org">外链</a>，<a href="#local">锚点</a>。</p>
  <p><img src="../images/pic.png" alt="图"/></p>
  <p><a href="javascript:alert(1)">坏链接</a></p>
  <iframe src="https://evil.example"></iframe>
</body>
</html>`)

	spine := map[string]int{
		"oebps/text/ch02.xhtml": 1,
		"oebps/text/ch03.xhtml": 2,
	}
	doc := renderChapter("book1", 1, "OEBPS/text/ch02.xhtml", raw, spine, "", "zh-CN")

	if strings.Contains(doc, "<script") || strings.Contains(doc, "alert('xss')") {
		t.Error("script 未被移除")
	}
	if strings.Contains(strings.ToLower(doc), "onload=") || strings.Contains(strings.ToLower(doc), "onclick=") {
		t.Error("内联事件未被移除")
	}
	if strings.Contains(doc, "javascript:") {
		t.Error("javascript: 链接未被移除")
	}
	if !strings.Contains(doc, `href="#moyue-chapter-2#next"`) {
		t.Error("章节内链未被转换为阅读器跳转，或锚点片段丢失")
	}
	if !strings.Contains(doc, `href="https://example.org"`) {
		t.Error("外部链接不应被改写")
	}
	if !strings.Contains(doc, `href="#local"`) {
		t.Error("同文档锚点不应被改写")
	}
	if !strings.Contains(doc, `/api/books/book1/resource?path=OEBPS%2Fimages%2Fpic.png`) {
		t.Error("图片地址未改写")
	}
	if !strings.Contains(doc, `/api/books/book1/resource?path=OEBPS%2Fstyles%2Fbook.css`) {
		t.Error("样式表地址未改写")
	}
	if !strings.Contains(doc, `<meta name="viewport"`) {
		t.Error("缺少 viewport")
	}
	if strings.Contains(doc, "iframe") {
		t.Log("iframe 保留（由 CSP 阻止执行）")
	}
}

func TestRenderChapterRewritesSrcsetAndSVG(t *testing.T) {
	raw := []byte(`<html><body>
  <img src="../images/a.png" srcset="../images/a.png 1x, ../images/a@2x.png 2x">
  <svg><image xlink:href="../images/svg.png"/></svg>
  <a href="https://example.org/&quot;onmouseover=alert(1)">x</a>
</body></html>`)
	doc := renderChapter("book1", 1, "OEBPS/text/ch02.xhtml", raw, map[string]int{}, "", "zh-CN")
	for _, want := range []string{
		"OEBPS%2Fimages%2Fa.png",
		"OEBPS%2Fimages%2Fa%402x.png",
		"OEBPS%2Fimages%2Fsvg.png",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing rewritten resource %q in %s", want, doc)
		}
	}
	if strings.Contains(doc, `"onmouseover=`) {
		t.Errorf("URL attribute was not escaped: %s", doc)
	}
}

func TestRewriteCSS(t *testing.T) {
	ctx := &renderCtx{bookID: "b1", spine: map[string]int{}, dir: "OEBPS/styles"}
	in := `@import "../styles/base.css"; @font-face{src:url("../fonts/a.woff2")} body{background:url('data:image/png;base64,xx')}
	div{background:url(https://cdn.example/x.png)} span{background:url(../images/bg.jpg)}`
	out := rewriteCSS(in, ctx)

	if !strings.Contains(out, `url("/api/books/b1/resource?path=OEBPS%2Ffonts%2Fa.woff2")`) {
		t.Errorf("字体地址未改写: %s", out)
	}
	if !strings.Contains(out, "data:image/png;base64,xx") {
		t.Error("data URI 不应被改写")
	}
	if !strings.Contains(out, "https://cdn.example/x.png") {
		t.Error("绝对地址不应被改写")
	}
	if !strings.Contains(out, `/api/books/b1/resource?path=OEBPS%2Fstyles%2Fbase.css`) {
		t.Errorf("@import 未改写: %s", out)
	}
	if !strings.Contains(out, `url("/api/books/b1/resource?path=OEBPS%2Fimages%2Fbg.jpg")`) {
		t.Error("背景图地址未改写")
	}
}

func TestCleanPathBlocksTraversal(t *testing.T) {
	cases := map[string]string{
		"../secret":          "secret",
		"/a/../../b.txt":     "b.txt",
		"OEBPS/images/a.jpg": "OEBPS/images/a.jpg",
	}
	for in, want := range cases {
		if got := cleanPath(in); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRenderChapterDefeatsNestedScript 覆盖嵌套 <script> 的重建攻击：
// 只替换一轮时，删掉内层标签后残片会拼成新的可执行 <script>。
func TestRenderChapterDefeatsNestedScript(t *testing.T) {
	cases := []struct{ name, in string }{
		{"嵌套 script", `<p><scr<script>x</script>ipt>alert(2)</script></p>`},
		{"三层嵌套", `<p>a<scr<scr<script>y</script>ipt>z</script>ipt>alert(3)</script></p>`},
		{"自闭合", `<p><script src="//evil"/><b>x</b></p>`},
		{"大小写混合", `<p><ScRipT>alert(5)</sCrIpT></p>`},
	}
	spine := map[string]int{"oebps/c1.xhtml": 0}
	for _, c := range cases {
		doc := renderChapter("book1", 0, "oebps/c1.xhtml",
			[]byte("<html><body>"+c.in+"</body></html>"), spine, "", "zh-CN")
		if strings.Contains(strings.ToLower(doc), "<script") {
			t.Errorf("%s: 清洗后仍重建出 script 标签: %s", c.name, doc)
		}
	}
	// 反向用例：正文里出现 script 字样不得被误删
	doc := renderChapter("book1", 0, "", []byte(`<p>提到 script 关键字是安全的</p>`), spine, "", "zh-CN")
	if !strings.Contains(doc, "script 关键字") {
		t.Error("正文被误伤")
	}
}
