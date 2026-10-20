// Package epub 提供零依赖的 EPUB 2 / EPUB 3 解析能力。
//
// 解析结果包含书籍元信息、阅读顺序（spine）、目录（EPUB3 nav 或 EPUB2 NCX）
// 以及封面路径。所有路径均为 zip 内部的“绝对”路径（以 / 分隔，不含前导斜杠），
// 便于后续按路径读取资源。
package epub

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// ErrInvalid 表示文件不是可解析的 EPUB。
var ErrInvalid = errors.New("不是有效的 EPUB 文件")

// Chapter 是阅读顺序中的一个文档。
type Chapter struct {
	Index int    `json:"index"`
	Href  string `json:"href"`
	Title string `json:"title"`
}

// TOCItem 是目录中的一项。Chapter 为 -1 表示目录项没有对应的正文文档（例如仅锚点）。
type TOCItem struct {
	Title   string `json:"title"`
	Href    string `json:"href"`
	Level   int    `json:"level"`
	Chapter int    `json:"chapter"`
}

// Book 是一本已解析的电子书。
type Book struct {
	Title       string
	Creator     string
	Language    string
	Publisher   string
	Description string
	Date        string
	Identifier  string
	CoverHref   string
	Chapters    []Chapter
	TOC         []TOCItem
}

type containerDoc struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfDoc struct {
	Metadata struct {
		Title       string   `xml:"title"`
		Creators    []string `xml:"creator"`
		Language    string   `xml:"language"`
		Publisher   string   `xml:"publisher"`
		Description string   `xml:"description"`
		Date        string   `xml:"date"`
		Identifier  string   `xml:"identifier"`
		Metas       []struct {
			Name     string `xml:"name,attr"`
			Content  string `xml:"content,attr"`
			Property string `xml:"property,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Toc  string `xml:"toc,attr"`
		Refs []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
	Guide struct {
		Refs []struct {
			Type string `xml:"type,attr"`
			Href string `xml:"href,attr"`
		} `xml:"reference"`
	} `xml:"guide"`
}

type ncxDoc struct {
	NavMap struct {
		Points []ncxPoint `xml:"navPoint"`
	} `xml:"navMap"`
}

type ncxPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxPoint `xml:"navPoint"`
}

// zipReader 封装 zip 内的文件查找，兼容大小写与 URL 转义不一致的情况。
type zipReader struct {
	files map[string]*zip.File
}

func (z *zipReader) lookup(name string) *zip.File {
	name = normalize(name)
	if f, ok := z.files[name]; ok {
		return f
	}
	for key, f := range z.files {
		if strings.EqualFold(key, name) {
			return f
		}
	}
	return nil
}

func (z *zipReader) read(name string) ([]byte, error) {
	f := z.lookup(name)
	if f == nil {
		return nil, fmt.Errorf("epub: 找不到内部文件 %q", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 32<<20))
}

// Open 解析指定路径的 EPUB 文件。解析完成后不会持有文件句柄。
func Open(file string) (*Book, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	defer zr.Close()

	z := &zipReader{files: make(map[string]*zip.File, len(zr.File))}
	for _, f := range zr.File {
		name := normalize(f.Name)
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		z.files[name] = f
	}

	raw, err := z.read("META-INF/container.xml")
	if err != nil {
		return nil, ErrInvalid
	}
	var container containerDoc
	if err := xml.Unmarshal(raw, &container); err != nil || len(container.Rootfiles) == 0 {
		return nil, ErrInvalid
	}

	opfPath := normalize(container.Rootfiles[0].FullPath)
	raw, err = z.read(opfPath)
	if err != nil {
		return nil, ErrInvalid
	}
	var opf opfDoc
	if err := xml.Unmarshal(raw, &opf); err != nil {
		return nil, fmt.Errorf("%w: 无法解析 OPF: %v", ErrInvalid, err)
	}

	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	byID := make(map[string]struct {
		Href       string
		MediaType  string
		Properties string
	}, len(opf.Manifest.Items))
	for _, it := range opf.Manifest.Items {
		byID[it.ID] = struct {
			Href       string
			MediaType  string
			Properties string
		}{it.Href, it.MediaType, it.Properties}
	}

	b := &Book{
		Title:       strings.TrimSpace(opf.Metadata.Title),
		Creator:     strings.TrimSpace(strings.Join(opf.Metadata.Creators, "、")),
		Language:    strings.TrimSpace(opf.Metadata.Language),
		Publisher:   strings.TrimSpace(opf.Metadata.Publisher),
		Description: cleanText(opf.Metadata.Description),
		Date:        strings.TrimSpace(opf.Metadata.Date),
		Identifier:  strings.TrimSpace(opf.Metadata.Identifier),
	}

	// spine：阅读顺序
	for _, ref := range opf.Spine.Refs {
		it, ok := byID[ref.IDRef]
		if !ok || strings.EqualFold(ref.Linear, "no") {
			continue
		}
		href := resolve(opfDir, it.Href)
		b.Chapters = append(b.Chapters, Chapter{Index: len(b.Chapters), Href: href})
	}
	if len(b.Chapters) == 0 {
		// 兜底：按 manifest 顺序收集 XHTML 文档
		for _, it := range opf.Manifest.Items {
			mt := strings.ToLower(it.MediaType)
			if mt == "application/xhtml+xml" || mt == "text/html" {
				b.Chapters = append(b.Chapters, Chapter{Index: len(b.Chapters), Href: resolve(opfDir, it.Href)})
			}
		}
	}
	if len(b.Chapters) == 0 {
		return nil, ErrInvalid
	}

	// 目录：优先 EPUB3 nav，其次 NCX
	for _, it := range opf.Manifest.Items {
		if strings.Contains(" "+strings.ToLower(it.Properties)+" ", " nav ") {
			navPath := resolve(opfDir, it.Href)
			if raw, err := z.read(navPath); err == nil {
				b.TOC = parseNav(raw, path.Dir(navPath))
			}
			if len(b.TOC) > 0 {
				break
			}
		}
	}
	if len(b.TOC) == 0 {
		ncxPath := ""
		if opf.Spine.Toc != "" {
			if it, ok := byID[opf.Spine.Toc]; ok {
				ncxPath = resolve(opfDir, it.Href)
			}
		}
		if ncxPath == "" {
			for _, it := range opf.Manifest.Items {
				if strings.Contains(strings.ToLower(it.MediaType), "dtbncx") {
					ncxPath = resolve(opfDir, it.Href)
					break
				}
			}
		}
		if ncxPath != "" {
			if raw, err := z.read(ncxPath); err == nil {
				b.TOC = parseNCX(raw, path.Dir(ncxPath))
			}
		}
	}

	// 目录项 -> 章节序号
	index := make(map[string]int, len(b.Chapters))
	for i, ch := range b.Chapters {
		index[strings.ToLower(ch.Href)] = i
		index[strings.ToLower(stripFragment(ch.Href))] = i
	}
	for i := range b.TOC {
		b.TOC[i].Chapter = -1
		if n, ok := index[strings.ToLower(stripFragment(b.TOC[i].Href))]; ok {
			b.TOC[i].Chapter = n
			if b.Chapters[n].Title == "" {
				b.Chapters[n].Title = b.TOC[i].Title
			}
		}
	}
	for i := range b.Chapters {
		if b.Chapters[i].Title == "" {
			b.Chapters[i].Title = fmt.Sprintf("第 %d 节", i+1)
		}
	}
	if len(b.TOC) == 0 {
		for _, ch := range b.Chapters {
			b.TOC = append(b.TOC, TOCItem{Title: ch.Title, Href: ch.Href, Chapter: ch.Index})
		}
	}

	// 封面
	b.CoverHref = findCover(opf, opfDir, byID)

	return b, nil
}

func findCover(opf opfDoc, opfDir string, byID map[string]struct {
	Href       string
	MediaType  string
	Properties string
}) string {
	for _, it := range opf.Manifest.Items {
		if strings.Contains(strings.ToLower(it.Properties), "cover-image") {
			return resolve(opfDir, it.Href)
		}
	}
	for _, m := range opf.Metadata.Metas {
		if !strings.EqualFold(m.Name, "cover") {
			continue
		}
		if it, ok := byID[strings.TrimSpace(m.Content)]; ok {
			return resolve(opfDir, it.Href)
		}
	}
	for _, ref := range opf.Guide.Refs {
		if strings.EqualFold(ref.Type, "cover") {
			return resolve(opfDir, ref.Href)
		}
	}
	// 兜底：文件名里带 cover 的图片
	for _, it := range opf.Manifest.Items {
		lower := strings.ToLower(it.Href)
		if strings.HasPrefix(strings.ToLower(it.MediaType), "image/") &&
			(strings.Contains(lower, "cover") || strings.Contains(lower, "front")) {
			return resolve(opfDir, it.Href)
		}
	}
	return ""
}

var (
	navBlockRe = regexp.MustCompile(`(?is)<nav\b([^>]*)>(.*?)</nav\s*>`)
	navTokenRe = regexp.MustCompile(`(?is)<ol\b[^>]*>|</ol\s*>|<a\b([^>]*)>(.*?)</a\s*>`)
	hrefRe     = regexp.MustCompile(`(?is)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	tagRe      = regexp.MustCompile(`(?s)<[^>]+>`)
	blockEndRe = regexp.MustCompile(`(?is)<\s*(?:br|/p|/div|/li|/h[1-6]|/tr|/blockquote)\s*/?>`)
	spaceRe    = regexp.MustCompile(`\s+`)
)

func parseNav(raw []byte, baseDir string) []TOCItem {
	doc := string(raw)
	blocks := navBlockRe.FindAllStringSubmatch(doc, -1)
	var body string
	for _, blk := range blocks {
		if strings.Contains(strings.ToLower(blk[1]), "toc") {
			body = blk[2]
			break
		}
	}
	if body == "" && len(blocks) > 0 {
		body = blocks[0][2]
	}
	if body == "" {
		return nil
	}

	var items []TOCItem
	depth := 0
	for _, m := range navTokenRe.FindAllStringSubmatch(body, -1) {
		full := strings.ToLower(m[0])
		switch {
		case strings.HasPrefix(full, "<ol"):
			depth++
		case strings.HasPrefix(full, "</ol"):
			if depth > 0 {
				depth--
			}
		default:
			attrs, inner := m[1], m[2]
			hm := hrefRe.FindStringSubmatch(attrs)
			if hm == nil {
				continue
			}
			href := hm[1]
			if href == "" {
				href = hm[2]
			}
			title := cleanText(inner)
			if title == "" || strings.HasPrefix(href, "javascript:") {
				continue
			}
			level := depth - 1
			if level < 0 {
				level = 0
			}
			items = append(items, TOCItem{
				Title: title,
				Href:  resolve(baseDir, href),
				Level: level,
			})
		}
	}
	return items
}

func parseNCX(raw []byte, baseDir string) []TOCItem {
	var doc ncxDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	var items []TOCItem
	var walk func(points []ncxPoint, level int)
	walk = func(points []ncxPoint, level int) {
		for _, p := range points {
			title := strings.TrimSpace(p.Label.Text)
			src := strings.TrimSpace(p.Content.Src)
			if title != "" && src != "" {
				items = append(items, TOCItem{
					Title: title,
					Href:  resolve(baseDir, src),
					Level: level,
				})
			}
			if len(p.Children) > 0 {
				walk(p.Children, level+1)
			}
		}
	}
	walk(doc.NavMap.Points, 0)
	return items
}

// normalize 统一 zip 内路径写法。
func normalize(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "/")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	// 去掉可能存在的 "./" 前缀
	p = strings.TrimPrefix(p, "./")
	return p
}

func unescapeHref(href string) string {
	if strings.Contains(href, "%") {
		if u, err := url.PathUnescape(href); err == nil {
			return u
		}
	}
	return href
}

// resolve 把 href 相对 baseDir 解析成 zip 内绝对路径，并保留片段。
func resolve(baseDir, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	frag := ""
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href, frag = href[:i], href[i:]
	}
	if i := strings.IndexByte(href, '?'); i >= 0 {
		href = href[:i]
	}
	href = unescapeHref(href)
	var full string
	if strings.HasPrefix(href, "/") {
		full = normalize(href)
	} else {
		full = normalize(path.Join(baseDir, href))
	}
	return full + frag
}

func stripFragment(p string) string {
	if i := strings.IndexByte(p, '#'); i >= 0 {
		return p[:i]
	}
	return p
}

// cleanText 去掉标签、压缩空白，并截断过长的简介。
func cleanText(s string) string {
	if s == "" {
		return ""
	}
	s = blockEndRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 400 {
		s = string(r[:400]) + "…"
	}
	return s
}
