package api

import (
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	scriptRe    = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<script\b[^>]*/>`)
	eventAttrRe = regexp.MustCompile(`(?is)\son[a-z]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
	jsHrefRe    = regexp.MustCompile(`(?is)\shref\s*=\s*["']\s*javascript:[^"']*["']`)
	attrURLRe   = regexp.MustCompile(`(?is)(\s(?:src|href|poster|data-src|xlink:href)\s*=\s*)("([^"]*)"|'([^']*)')`)
	srcsetRe    = regexp.MustCompile(`(?is)(\s(?:srcset|imagesrcset)\s*=\s*)("([^"]*)"|'([^']*)')`)
	cssURLRe    = regexp.MustCompile(`(?is)url\(\s*["']?([^"')]+)["']?\s*\)`)
	cssImportRe = regexp.MustCompile(`(?is)(@import\s+)(["'])([^"']+)(["'])`)
	headRe      = regexp.MustCompile(`(?is)<head\b[^>]*>(.*?)</head\s*>`)
	bodyRe      = regexp.MustCompile(`(?is)<body\b[^>]*>(.*?)(?:</body\s*>|$)`)
	titleRe     = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title\s*>`)
	headKeepRe  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>|<link\b[^>]*/?>`)
	styleBodyRe = regexp.MustCompile(`(?is)(<style\b[^>]*>)(.*?)(</style\s*>)`)
	anyTagRe    = regexp.MustCompile(`(?s)<[^>]+>`)
)

// baseCSS 是章节文档自带的基础排版，页面加载后前端还会再注入一层可调样式。
const baseCSS = `
html{background:transparent;-webkit-text-size-adjust:100%;}
body{margin:0;padding:10px clamp(20px,4.6vw,60px) 96px;font-family:"Songti SC","Source Han Serif SC","Noto Serif CJK SC",Georgia,serif;
font-size:19px;line-height:1.9;color:#26221c;background:transparent;overflow-wrap:break-word;word-break:break-word;letter-spacing:.01em;}
img,svg,video{max-width:100%!important;height:auto!important;}
img{display:block;margin:1.4em auto;}
table{max-width:100%;border-collapse:collapse;font-size:.95em;}
td,th{padding:.3em .6em;}
pre{white-space:pre-wrap;overflow-x:auto;font-size:.86em;line-height:1.6;}
code,kbd,samp{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em;}
h1,h2,h3,h4,h5,h6{line-height:1.45;margin:1.6em 0 .8em;}
p{margin:0 0 1em;}
ul,ol{padding-left:1.5em;}
blockquote{margin:1.2em 0;padding:.2em 0 .2em 1em;border-left:3px solid rgba(0,0,0,.16);color:rgba(0,0,0,.66);}
a{color:inherit;text-decoration:underline;text-decoration-color:rgba(0,0,0,.28);text-underline-offset:3px;}
hr{border:0;border-top:1px solid rgba(0,0,0,.12);margin:2em 0;}
::selection{background:rgba(47,93,80,.18);}
`

type renderCtx struct {
	bookID string
	spine  map[string]int
	dir    string
}

// renderChapter 把原始 XHTML 转换为可在 iframe 中安全展示的独立文档。
func renderChapter(bookID string, chapterIndex int, chapterHref string, raw []byte, spine map[string]int, title, lang string) string {
	s := string(raw)

	// 基础清洗：脚本、内联事件、javascript: 链接
	s = scriptRe.ReplaceAllString(s, "")
	s = eventAttrRe.ReplaceAllString(s, "")
	s = jsHrefRe.ReplaceAllString(s, ` href="#"`)

	if t := titleRe.FindStringSubmatch(s); len(t) == 2 && strings.TrimSpace(title) == "" {
		title = stripTags(t[1])
	}

	head := ""
	if m := headRe.FindStringSubmatch(s); len(m) == 2 {
		head = m[1]
	}
	body := ""
	if m := bodyRe.FindStringSubmatch(s); len(m) == 2 {
		body = m[1]
	} else {
		body = s
	}

	ctx := &renderCtx{bookID: bookID, spine: spine, dir: path.Dir(chapterHref)}
	if ctx.dir == "." {
		ctx.dir = ""
	}
	head = ctx.rewriteHTML(head)
	body = ctx.rewriteHTML(body)
	head = ctx.rewriteSrcset(head)
	body = ctx.rewriteSrcset(body)

	// 只保留样式相关的 head 内容，避免 meta refresh 等副作用
	var kept strings.Builder
	for _, tag := range headKeepRe.FindAllString(head, -1) {
		kept.WriteString(tag)
		kept.WriteByte('\n')
	}
	keptHead := styleBodyRe.ReplaceAllStringFunc(kept.String(), func(block string) string {
		m := styleBodyRe.FindStringSubmatch(block)
		if len(m) != 4 {
			return block
		}
		return m[1] + rewriteCSS(m[2], ctx) + m[3]
	})

	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("第 %d 节", chapterIndex+1)
	}
	if lang == "" {
		lang = "zh-CN"
	}

	var sb strings.Builder
	sb.Grow(len(body) + 2048)
	sb.WriteString("<!doctype html><html lang=\"")
	sb.WriteString(html.EscapeString(lang))
	sb.WriteString("\"><head><meta charset=\"utf-8\">")
	sb.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, viewport-fit=cover\">")
	sb.WriteString("<title>")
	sb.WriteString(html.EscapeString(title))
	sb.WriteString("</title>")
	sb.WriteString(keptHead)
	sb.WriteString("<style id=\"moyue-base\">")
	sb.WriteString(baseCSS)
	sb.WriteString("</style></head><body>")
	sb.WriteString(body)
	sb.WriteString("</body></html>")
	return sb.String()
}

func (c *renderCtx) rewriteHTML(s string) string {
	return attrURLRe.ReplaceAllStringFunc(s, func(match string) string {
		m := attrURLRe.FindStringSubmatch(match)
		if len(m) != 5 {
			return match
		}
		quote := `"`
		raw := m[3]
		if m[2][0] == '\'' {
			quote = `'`
			raw = m[4]
		}
		return m[1] + quote + c.url(raw) + quote
	})
}

func (c *renderCtx) rewriteSrcset(s string) string {
	return srcsetRe.ReplaceAllStringFunc(s, func(match string) string {
		m := srcsetRe.FindStringSubmatch(match)
		if len(m) != 5 {
			return match
		}
		quote := `"`
		raw := m[3]
		if m[2][0] == '\'' {
			quote = `'`
			raw = m[4]
		}
		parts := strings.Split(raw, ",")
		for i, part := range parts {
			fields := strings.Fields(part)
			if len(fields) == 0 {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(fields[0]), "data:") {
				fields[0] = c.url(fields[0])
			}
			parts[i] = strings.Join(fields, " ")
		}
		return m[1] + quote + strings.Join(parts, ", ") + quote
	})
}

func (c *renderCtx) url(raw string) string {
	val := strings.TrimSpace(html.UnescapeString(raw))
	if val == "" {
		return raw
	}
	lower := strings.ToLower(val)
	if strings.HasPrefix(lower, "javascript:") {
		return "#"
	}
	if strings.HasPrefix(val, "#") || strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(lower, "blob:") {
		return html.EscapeString(val)
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(val, "//") {
		return html.EscapeString(val)
	}

	frag := ""
	if i := strings.IndexByte(val, '#'); i >= 0 {
		val, frag = val[:i], val[i:]
	}
	if i := strings.IndexByte(val, '?'); i >= 0 {
		val = val[:i]
	}
	decoded, err := url.PathUnescape(val)
	if err != nil {
		decoded = val
	}
	if strings.HasPrefix(decoded, "/api/") {
		return html.EscapeString(val)
	}
	var full string
	if strings.HasPrefix(decoded, "/") {
		full = cleanPath(decoded)
	} else {
		full = cleanPath(path.Join(c.dir, decoded))
	}

	if idx, ok := c.spine[strings.ToLower(full)]; ok {
		return fmt.Sprintf("#moyue-chapter-%d", idx)
	}
	// 指向 spine 之外的 XHTML（如脚注页）不做跳转，避免把原始文档直接装进 iframe
	switch strings.ToLower(path.Ext(full)) {
	case ".xhtml", ".html", ".htm":
		return "#"
	}
	return html.EscapeString("/api/books/" + c.bookID + "/resource?path=" + url.QueryEscape(full) + frag)
}

func rewriteCSS(css string, ctx *renderCtx) string {
	css = cssImportRe.ReplaceAllStringFunc(css, func(match string) string {
		m := cssImportRe.FindStringSubmatch(match)
		if len(m) != 5 {
			return match
		}
		raw := strings.TrimSpace(m[3])
		lower := strings.ToLower(raw)
		if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "http://") ||
			strings.HasPrefix(lower, "https://") || strings.HasPrefix(raw, "//") {
			return match
		}
		decoded, err := url.PathUnescape(raw)
		if err != nil {
			decoded = raw
		}
		full := cleanPath(path.Join(ctx.dir, decoded))
		return m[1] + m[2] + "/api/books/" + ctx.bookID + "/resource?path=" +
			url.QueryEscape(full) + m[4]
	})
	return cssURLRe.ReplaceAllStringFunc(css, func(match string) string {
		m := cssURLRe.FindStringSubmatch(match)
		if len(m) != 2 {
			return match
		}
		raw := strings.TrimSpace(m[1])
		lower := strings.ToLower(raw)
		if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "http://") ||
			strings.HasPrefix(lower, "https://") || strings.HasPrefix(raw, "//") ||
			strings.HasPrefix(raw, "#") {
			return match
		}
		decoded, err := url.PathUnescape(raw)
		if err != nil {
			decoded = raw
		}
		full := cleanPath(path.Join(ctx.dir, decoded))
		return `url("/api/books/` + ctx.bookID + `/resource?path=` + url.QueryEscape(full) + `")`
	})
}

func cleanPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

func stripTags(s string) string {
	s = anyTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// renderTXTChapter 把 TXT 章节包装成与 EPUB 章节一致的阅读文档。
func renderTXTChapter(title, body, lang string) string {
	if strings.TrimSpace(title) == "" {
		title = "正文"
	}
	if lang == "" {
		lang = "zh-CN"
	}
	var sb strings.Builder
	sb.Grow(len(body) + 2048)
	sb.WriteString("<!doctype html><html lang=\"")
	sb.WriteString(html.EscapeString(lang))
	sb.WriteString("\"><head><meta charset=\"utf-8\">")
	sb.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, viewport-fit=cover\">")
	sb.WriteString("<title>")
	sb.WriteString(html.EscapeString(title))
	sb.WriteString("</title><style id=\"moyue-base\">")
	sb.WriteString(baseCSS)
	sb.WriteString(`
body { text-align: left; }
h1.txt-title { text-align:center; font-size:1.45em; font-weight:600; letter-spacing:.12em;
  margin:1.6em 0 2.2em; color:inherit; }
p.txt-sep { text-align:center; text-indent:0; color:#a08c6a; letter-spacing:.5em; margin:2em 0; }
`)
	sb.WriteString("</style></head><body><h1 class=\"txt-title\">")
	sb.WriteString(html.EscapeString(title))
	sb.WriteString("</h1>")
	sb.WriteString(body)
	sb.WriteString("</body></html>")
	return sb.String()
}

// renderComicChapter 把漫画包的一页（或相邻两页）包装成阅读文档。
func renderComicChapter(bookID string, index int, title string, spread bool) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = fmt.Sprintf("第 %d 页", index+1)
	}
	var sb strings.Builder
	sb.WriteString("<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	sb.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, viewport-fit=cover\">")
	sb.WriteString("<title>")
	sb.WriteString(html.EscapeString(title))
	sb.WriteString("</title><style>")
	sb.WriteString(`
html,body{height:100%;}
body{margin:0;padding:8px;box-sizing:border-box;background:transparent;display:flex;
align-items:center;justify-content:center;gap:8px;overflow:auto;}
.comic-page{flex:1 1 0;min-width:0;height:100%;display:flex;align-items:center;justify-content:center;}
.comic-page img{max-width:100%;max-height:100%;width:auto;height:auto;display:block;
box-shadow:0 10px 36px rgba(20,18,12,.2),0 1px 2px rgba(20,18,12,.15);border-radius:1px;
user-select:none;-webkit-user-drag:none;}
`)
	sb.WriteString("</style></head><body>")
	pages := []int{index}
	if spread {
		pages = append(pages, index+1)
	}
	for _, p := range pages {
		sb.WriteString("<div class=\"comic-page\"><img src=\"/api/books/")
		sb.WriteString(bookID)
		sb.WriteString("/page/")
		sb.WriteString(fmt.Sprintf("%d", p))
		sb.WriteString("\" alt=\"")
		sb.WriteString(html.EscapeString(fmt.Sprintf("第 %d 页", p+1)))
		sb.WriteString("\" draggable=\"false\"></div>")
	}
	sb.WriteString("</body></html>")
	return sb.String()
}
