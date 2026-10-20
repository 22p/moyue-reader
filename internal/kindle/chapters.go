package kindle

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Chapter 是拆分后的 Kindle 章节。
type Chapter struct {
	Title string
	HTML  string
}

var (
	reXMLDecl   = regexp.MustCompile(`(?is)<\?xml[^>]*\?>`)
	reDoctype   = regexp.MustCompile(`(?is)<!DOCTYPE[^>]*>`)
	reHead      = regexp.MustCompile(`(?is)<head\b[^>]*>.*?</head>`)
	reHTMLOpen  = regexp.MustCompile(`(?is)<html\b[^>]*>`)
	reHTMLClose = regexp.MustCompile(`(?is)</html>`)
	reBodyOpen  = regexp.MustCompile(`(?is)<body\b[^>]*>`)
	reBodyClose = regexp.MustCompile(`(?is)</body>`)
	rePageBreak = regexp.MustCompile(`(?is)<mbp:pagebreak\s*/?>`)
	reH1        = regexp.MustCompile(`(?is)<h1\b`)
	reH2        = regexp.MustCompile(`(?is)<h2\b`)
	reTag       = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace     = regexp.MustCompile(`\s+`)
	reImgTag    = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	reRecIndex  = regexp.MustCompile(`(?i)recindex\s*=\s*["']?(\d+)`)
	reSrcAttr   = regexp.MustCompile(`(?is)src\s*=\s*["']([^"']*)["']`)
)

// SplitChapters 把整本 HTML 拆成章节，并把图片地址重写到服务端接口。
func (b *Book) SplitChapters(bookID string) []Chapter {
	text := normalize(b.HTML)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	parts := splitBy(text, reH2, 3)
	if len(parts) < 2 {
		parts = splitBy(text, reH1, 2)
	}
	if len(parts) < 2 && len(rePageBreak.FindAllStringIndex(text, -1)) >= 2 {
		parts = splitByTag(text, rePageBreak)
	}
	if len(parts) == 0 {
		parts = []string{text}
	}

	out := make([]Chapter, 0, len(parts))
	for i, part := range parts {
		part = rePageBreak.ReplaceAllString(part, `<hr class="kindle-break">`)
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		title := chapterTitle(part)
		if title == "" {
			if i == 0 {
				title = "前言"
			} else {
				title = fmt.Sprintf("第 %d 节", len(out)+1)
			}
		}
		part = rewriteImages(part, bookID)
		body := "<!doctype html><html><head><meta charset=\"utf-8\"><title>" +
			html.EscapeString(title) + "</title></head><body>" + part + "</body></html>"
		out = append(out, Chapter{Title: title, HTML: body})
	}
	return out
}

func normalize(s string) string {
	s = reXMLDecl.ReplaceAllString(s, "")
	s = reDoctype.ReplaceAllString(s, "")
	s = reHead.ReplaceAllString(s, "")
	s = reHTMLOpen.ReplaceAllString(s, "")
	s = reHTMLClose.ReplaceAllString(s, "")
	s = reBodyOpen.ReplaceAllString(s, "")
	s = reBodyClose.ReplaceAllString(s, "")
	return s
}

func splitBy(text string, re *regexp.Regexp, min int) []string {
	idx := re.FindAllStringIndex(text, -1)
	if len(idx) < min {
		return nil
	}
	return cutAt(text, idx)
}

func splitByTag(text string, re *regexp.Regexp) []string {
	idx := re.FindAllStringIndex(text, -1)
	if len(idx) < 2 {
		return nil
	}
	return cutAt(text, idx)
}

func cutAt(text string, idx [][]int) []string {
	var parts []string
	start := 0
	for i, pos := range idx {
		if i == 0 {
			if pos[0] > 0 {
				parts = append(parts, text[:pos[0]])
			}
			start = pos[0]
			continue
		}
		parts = append(parts, text[start:pos[0]])
		start = pos[0]
	}
	parts = append(parts, text[start:])
	return parts
}

func chapterTitle(s string) string {
	for _, re := range []*regexp.Regexp{reH1, reH2} {
		loc := re.FindStringIndex(s)
		if loc == nil {
			continue
		}
		rest := s[loc[0]:]
		end := strings.Index(strings.ToLower(rest), "</h1>")
		if strings.HasPrefix(strings.ToLower(rest), "<h2") {
			end = strings.Index(strings.ToLower(rest), "</h2>")
		}
		if end < 0 {
			continue
		}
		raw := rest[:end]
		raw = reTag.ReplaceAllString(raw, " ")
		raw = html.UnescapeString(raw)
		raw = strings.TrimSpace(reSpace.ReplaceAllString(raw, " "))
		if raw != "" && utf8.RuneCountInString(raw) <= 80 {
			return raw
		}
	}
	return ""
}

func rewriteImages(s, bookID string) string {
	return reImgTag.ReplaceAllStringFunc(s, func(tag string) string {
		if m := reRecIndex.FindStringSubmatch(tag); len(m) == 2 {
			n, err := strconv.Atoi(m[1])
			if err == nil && n > 0 {
				return `<img src="/api/books/` + bookID + `/kindleimage/` + strconv.Itoa(n) + `" alt="">`
			}
		}
		if m := reSrcAttr.FindStringSubmatch(tag); len(m) == 2 {
			src := strings.TrimSpace(m[1])
			if strings.HasPrefix(strings.ToLower(src), "kindle:embed:") {
				hex := strings.TrimPrefix(strings.ToLower(src), "kindle:embed:")
				if i := strings.IndexAny(hex, "?&#"); i >= 0 {
					hex = hex[:i]
				}
				if n, err := strconv.ParseInt(hex, 16, 32); err == nil && n > 0 {
					return `<img src="/api/books/` + bookID + `/kindleimage/` + strconv.FormatInt(n, 10) + `" alt="">`
				}
			}
			if strings.HasPrefix(strings.ToLower(src), "kindle:") {
				return ""
			}
		}
		return tag
	})
}
