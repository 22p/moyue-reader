// Package fb2 解析 FictionBook 2 电子书，输出按章节切分的 HTML。
package fb2

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"strings"

	"golang.org/x/net/html/charset"
)

// Book 是解析后的 FB2。
type Book struct {
	ID          string
	Title       string
	Author      string
	Language    string
	Publisher   string
	Description string
	Date        string
	Cover       string
	Images      map[string][]byte
	ImageTypes  map[string]string
	Chapters    []Chapter
}

// Chapter 是一章 FB2 内容。
type Chapter struct {
	Title string
	HTML  string
}

// Open 读取并解析 FB2 文件。
func Open(path, bookID string) (*Book, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > 64<<20 {
		return nil, errors.New("FB2 文件超过 64MB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, bookID)
}

// Parse 解析 FB2 字节。
func Parse(data []byte, bookID string) (*Book, error) {
	enc, _, _ := charset.DetermineEncoding(data, "application/xml")
	decoded, err := io.ReadAll(enc.NewDecoder().Reader(bytes.NewReader(data)))
	if err != nil {
		return nil, err
	}

	root, err := parseTree(decoded)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Name != "FictionBook" {
		return nil, errors.New("不是有效的 FB2 文件")
	}

	book := &Book{
		ID:         bookID,
		Images:     map[string][]byte{},
		ImageTypes: map[string]string{},
		Language:   "zh-CN",
	}
	book.readDescription(root)
	for _, bin := range children(root, "binary") {
		id := attr(bin, "id")
		if id == "" {
			continue
		}
		raw := strings.TrimSpace(textOf(bin))
		if raw == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			continue
		}
		book.Images[id] = data
		ct := attr(bin, "content-type")
		if ct == "" {
			ct = "image/jpeg"
		}
		book.ImageTypes[id] = ct
	}
	if book.Cover == "" {
		for id := range book.Images {
			if strings.Contains(strings.ToLower(id), "cover") {
				book.Cover = id
				break
			}
		}
	}

	for _, body := range children(root, "body") {
		sections := children(body, "section")
		if len(sections) == 0 {
			html := renderChildren(body, 0, book)
			book.Chapters = append(book.Chapters, Chapter{Title: firstText(book, body), HTML: html})
			continue
		}
		for _, sec := range sections {
			title := sectionTitle(sec)
			if title == "" {
				title = fmt.Sprintf("第 %d 节", len(book.Chapters)+1)
			}
			book.Chapters = append(book.Chapters, Chapter{
				Title: title,
				HTML:  renderNode(sec, 1, book),
			})
		}
	}
	if len(book.Chapters) == 0 {
		return nil, errors.New("FB2 中没有正文")
	}
	return book, nil
}

func (b *Book) readDescription(root *xmlNode) {
	desc := child(root, "description")
	if desc == nil {
		return
	}
	info := child(desc, "title-info")
	if info != nil {
		b.Title = strings.TrimSpace(textOf(child(info, "book-title")))
		b.Language = strings.TrimSpace(textOf(child(info, "lang")))
		if b.Language == "" {
			b.Language = "zh-CN"
		}
		b.Description = strings.TrimSpace(textOf(child(info, "annotation")))
		var authors []string
		for _, a := range children(info, "author") {
			var parts []string
			for _, key := range []string{"first-name", "middle-name", "last-name"} {
				if v := strings.TrimSpace(textOf(child(a, key))); v != "" {
					parts = append(parts, v)
				}
			}
			name := strings.Join(parts, " ")
			if allCJK(parts) {
				name = strings.Join(parts, "")
			}
			if name == "" {
				name = strings.TrimSpace(textOf(child(a, "nickname")))
			}
			if name != "" {
				authors = append(authors, name)
			}
		}
		b.Author = strings.Join(authors, "、")
		if cp := child(info, "coverpage"); cp != nil {
			if img := child(cp, "image"); img != nil {
				b.Cover = strings.TrimPrefix(attr(img, "href"), "#")
			}
		}
	}
	if pub := child(desc, "publish-info"); pub != nil {
		b.Publisher = strings.TrimSpace(textOf(child(pub, "publisher")))
		b.Date = strings.TrimSpace(textOf(child(pub, "year")))
	}
}

func allCJK(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		for _, r := range part {
			if r < 0x2e80 {
				return false
			}
		}
	}
	return true
}

func sectionTitle(n *xmlNode) string {
	if t := child(n, "title"); t != nil {
		return strings.TrimSpace(textOf(t))
	}
	return ""
}

func firstText(b *Book, body *xmlNode) string {
	if t := sectionTitle(body); t != "" {
		return t
	}
	return "正文"
}

/* ------------------------------ XML 树 ------------------------------ */

type xmlNode struct {
	Name   string
	Attrs  map[string]string
	Items  []any // string 或 *xmlNode
	Parent *xmlNode
}

func parseTree(data []byte) (*xmlNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var root *xmlNode
	var stack []*xmlNode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xmlNode{Name: t.Name.Local, Attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.Attrs[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				n.Parent = parent
				parent.Items = append(parent.Items, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				cur := stack[len(stack)-1]
				cur.Items = append(cur.Items, string(t))
			}
		}
	}
	return root, nil
}

func child(n *xmlNode, name string) *xmlNode {
	for _, item := range n.Items {
		if c, ok := item.(*xmlNode); ok && c.Name == name {
			return c
		}
	}
	return nil
}

func children(n *xmlNode, name string) []*xmlNode {
	var out []*xmlNode
	for _, item := range n.Items {
		if c, ok := item.(*xmlNode); ok && c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func attr(n *xmlNode, name string) string {
	return n.Attrs[name]
}

func textOf(n *xmlNode) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*xmlNode)
	walk = func(cur *xmlNode) {
		for _, item := range cur.Items {
			switch v := item.(type) {
			case string:
				sb.WriteString(v)
			case *xmlNode:
				if v.Name != "image" {
					walk(v)
				}
			}
		}
	}
	walk(n)
	return sb.String()
}

/* ------------------------------ 渲染 ------------------------------ */

func renderChildren(n *xmlNode, level int, b *Book) string {
	var sb strings.Builder
	for _, item := range n.Items {
		switch v := item.(type) {
		case string:
			sb.WriteString(html.EscapeString(v))
		case *xmlNode:
			sb.WriteString(renderNode(v, level, b))
		}
	}
	return sb.String()
}

func renderNode(n *xmlNode, level int, b *Book) string {
	switch n.Name {
	case "section":
		title := sectionTitle(n)
		var sb strings.Builder
		if title != "" && level > 1 {
			tag := "h4"
			if level == 2 {
				tag = "h3"
			}
			sb.WriteString("<" + tag + ">" + html.EscapeString(title) + "</" + tag + ">")
		}
		for _, item := range n.Items {
			if c, ok := item.(*xmlNode); ok {
				if c.Name == "title" {
					continue
				}
				sb.WriteString(renderNode(c, level+1, b))
			}
		}
		return sb.String()
	case "title":
		tag := "h2"
		if level > 1 {
			tag = "h3"
		}
		return "<" + tag + ">" + html.EscapeString(strings.TrimSpace(textOf(n))) + "</" + tag + ">"
	case "subtitle":
		return "<h3>" + html.EscapeString(strings.TrimSpace(textOf(n))) + "</h3>"
	case "p":
		return "<p>" + renderChildren(n, level, b) + "</p>"
	case "v":
		return "<p class=\"fb2-v\">" + renderChildren(n, level, b) + "</p>"
	case "text-author":
		return "<p class=\"fb2-author\">" + renderChildren(n, level, b) + "</p>"
	case "emphasis":
		return "<em>" + renderChildren(n, level, b) + "</em>"
	case "strong":
		return "<strong>" + renderChildren(n, level, b) + "</strong>"
	case "strikethrough":
		return "<s>" + renderChildren(n, level, b) + "</s>"
	case "sub":
		return "<sub>" + renderChildren(n, level, b) + "</sub>"
	case "sup":
		return "<sup>" + renderChildren(n, level, b) + "</sup>"
	case "code":
		return "<code>" + renderChildren(n, level, b) + "</code>"
	case "a":
		href := attr(n, "href")
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
			return "<a href=\"" + html.EscapeString(href) + "\" target=\"_blank\" rel=\"noopener noreferrer\">" +
				renderChildren(n, level, b) + "</a>"
		}
		return "<span class=\"fb2-link\">" + renderChildren(n, level, b) + "</span>"
	case "image":
		id := strings.TrimPrefix(attr(n, "href"), "#")
		if id == "" || b.Images[id] == nil {
			return ""
		}
		return "<img src=\"/api/books/" + b.ID + "/fb2image?name=" + html.EscapeString(id) + "\" alt=\"\">"
	case "epigraph", "cite", "annotation":
		return "<blockquote>" + renderChildren(n, level, b) + "</blockquote>"
	case "poem":
		return "<div class=\"fb2-poem\">" + renderChildren(n, level, b) + "</div>"
	case "stanza":
		return "<div class=\"fb2-stanza\">" + renderChildren(n, level, b) + "</div>"
	case "empty-line":
		return "<p class=\"fb2-empty\"></p>"
	case "table":
		return "<table>" + renderChildren(n, level, b) + "</table>"
	case "tr":
		return "<tr>" + renderChildren(n, level, b) + "</tr>"
	case "td", "th":
		return "<" + n.Name + ">" + renderChildren(n, level, b) + "</" + n.Name + ">"
	default:
		return renderChildren(n, level, b)
	}
}
