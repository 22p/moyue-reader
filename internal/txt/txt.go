// Package txt 负责 TXT 小说的解码、元数据提取与自动分章。
package txt

import (
	"bytes"
	"errors"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// Chapter 是一章在全文中的字节区间。
type Chapter struct {
	Title string `json:"title"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Book 是解析后的 TXT。
type Book struct {
	Title    string
	Author   string
	Encoding string
	Text     string
	Chapters []Chapter
}

// ErrInvalid 表示文件内容不可用。
var ErrInvalid = errors.New("TXT 文件为空或无法识别编码")

var (
	reCNChapter  = regexp.MustCompile(`^(?:正文[\s　]*)?第[\s　]*[0-9０-９零〇一二三四五六七八九十百千万两]{1,12}[\s　]*[章节回卷集部篇话幕][^\n]{0,30}$`)
	reSpecial    = regexp.MustCompile(`^(?:序章|序言|自序|前言|楔子|引子|后记|尾声|终章|完结|番外|外传|附录|感言)(?:[\s　:：、.．\-—][^\n]{0,24})?$`)
	reEnglish    = regexp.MustCompile(`(?i)^(?:chapter|part|volume|book)\s+(?:[0-9]{1,4}|[ivxlcdm]{1,8}|[a-z]{2,12})(?:[\s:.\-—][^\n]{0,40})?$`)
	reSeparator  = regexp.MustCompile(`^[\s　]*(?:[*＊×xX·•\-—_=]{3,}|[·•。]{2,})[\s　]*$`)
	reParagraphs = regexp.MustCompile(`\n[ \t　]*\n`)

	reAuthorLine = regexp.MustCompile(`(?m)^[\s　]*(?:作[\s　]*者|著[\s　]*者)[:：][\s　]*([^\n\r]{1,30})[\s　]*$`)
	reTitleMark  = regexp.MustCompile(`^《([^》]{1,60})》[\s　]*(.*)$`)
	reByName     = regexp.MustCompile(`^(.*?)[\s　]*作[\s　]*者[:：][\s　]*(.{1,24})$`)
	reDashAuthor = regexp.MustCompile(`^(.{1,60}?)[\s　]*[-—_][\s　]*(.{1,20})$`)
)

// ParseFile 读取并解析 TXT 文件。
func ParseFile(path string) (*Book, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > 64<<20 {
		return nil, errors.New("TXT 文件超过 64MB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, filepath.Base(path))
}

// Parse 解码字节并切分章节。
func Parse(data []byte, filename string) (*Book, error) {
	text, encoding := Decode(data)
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrInvalid
	}

	title, author := metadata(filename, text)
	b := &Book{
		Title:    title,
		Author:   author,
		Encoding: encoding,
		Text:     text,
		Chapters: Split(text),
	}
	return b, nil
}

// Decode 按 BOM、UTF-8、GB18030、Big5 的顺序尝试解码。
func Decode(data []byte) (string, string) {
	if len(data) >= 3 && bytes.Equal(data[:3], []byte{0xEF, 0xBB, 0xBF}) {
		return string(data[3:]), "utf-8 (BOM)"
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return decodeUTF16(data[2:], true), "utf-16le"
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return decodeUTF16(data[2:], false), "utf-16be"
	}
	if utf8.Valid(data) {
		return string(data), "utf-8"
	}
	if out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
		text := string(out)
		if replacementRatio(text) < 0.02 {
			return text, "gb18030"
		}
	}
	if out, err := traditionalchinese.Big5.NewDecoder().Bytes(data); err == nil {
		return string(out), "big5"
	}
	return string(data), "unknown"
}

func decodeUTF16(b []byte, little bool) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if little {
			units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
		} else {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		}
	}
	return string(utf16.Decode(units))
}

func replacementRatio(s string) float64 {
	if s == "" {
		return 1
	}
	bad, total := 0, 0
	for _, r := range s {
		total++
		if r == utf8.RuneError {
			bad++
		}
	}
	return float64(bad) / float64(total)
}

// IsHeading 判断一行是否是章节标题。
func IsHeading(line string) bool {
	s := strings.TrimSpace(strings.TrimLeft(line, "　 \t"))
	if s == "" || strings.HasPrefix(s, "“") || strings.HasPrefix(s, "\"") {
		return false
	}
	if utf8.RuneCountInString(s) > 42 {
		return false
	}
	if strings.ContainsAny(s, "。！？!?；;") {
		return false
	}
	return reCNChapter.MatchString(s) || reSpecial.MatchString(s) || reEnglish.MatchString(s)
}

// Split 按章节标题切分全文；找不到标题时按段落长度分节。
func Split(text string) []Chapter {
	type head struct {
		title string
		pos   int
	}
	var heads []head

	pos := 0
	for pos <= len(text) {
		end := strings.IndexByte(text[pos:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += pos
		}
		line := text[pos:end]
		if IsHeading(line) {
			// 上一行若只是“第一卷”这类卷标、且与本章标题紧邻，则合并掉卷标
			if n := len(heads); n > 0 {
				between := text[heads[n-1].pos:pos]
				nonEmpty := 0
				for _, ln := range strings.Split(between, "\n") {
					if strings.TrimSpace(ln) != "" {
						nonEmpty++
					}
				}
				if nonEmpty <= 1 && pos-heads[n-1].pos < 500 {
					heads = heads[:n-1]
				}
			}
			heads = append(heads, head{title: cleanTitle(line), pos: pos})
		}
		if end >= len(text) {
			break
		}
		pos = end + 1
	}

	if len(heads) == 0 {
		return chunk(text)
	}

	var chapters []Chapter
	if first := heads[0].pos; strings.TrimSpace(text[:first]) != "" {
		pre := strings.TrimSpace(text[:first])
		if utf8.RuneCountInString(pre) >= 10 && !metadataOnly(pre) {
			chapters = append(chapters, Chapter{Title: "前言", Start: 0, End: first})
		}
	}
	for i, h := range heads {
		end := len(text)
		if i+1 < len(heads) {
			end = heads[i+1].pos
		}
		chapters = append(chapters, Chapter{Title: h.title, Start: h.pos, End: end})
	}

	// 丢弃只有标题没有正文的空章节
	out := chapters[:0]
	for _, c := range chapters {
		body := strings.TrimSpace(text[c.Start:c.End])
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			first := strings.TrimSpace(body[:i])
			if IsHeading(first) {
				body = strings.TrimSpace(body[i+1:])
			}
		} else if IsHeading(body) {
			body = ""
		}
		if body == "" && len(chapters) > 1 {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return chunk(text)
	}
	if len(out) == 1 && utf8.RuneCountInString(text) > 30000 {
		return chunk(text)
	}
	return out
}

// metadataOnly 判断序章之前的文字是否只有书名、作者等版权信息。
func metadataOnly(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "《") && strings.HasSuffix(t, "》") {
			continue
		}
		if reAuthorLine.MatchString(t) {
			continue
		}
		if strings.HasPrefix(t, "书名") || strings.HasPrefix(t, "简介") ||
			strings.HasPrefix(t, "内容简介") || strings.HasPrefix(t, "声明") {
			continue
		}
		return false
	}
	return true
}

func cleanTitle(line string) string {
	s := strings.TrimSpace(strings.TrimLeft(line, "　 \t"))
	s = strings.TrimPrefix(s, "正文")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "　 ")
	if utf8.RuneCountInString(s) > 60 {
		s = string([]rune(s)[:60]) + "…"
	}
	return s
}

func metadata(filename, text string) (string, string) {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	base = strings.TrimSpace(base)

	title, author := base, ""

	if m := reTitleMark.FindStringSubmatch(base); m != nil {
		title = strings.TrimSpace(m[1])
		rest := strings.TrimSpace(m[2])
		if rest != "" {
			author = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(rest, "作者"), "："))
			author = strings.TrimSpace(strings.Trim(author, "《》()（）"))
		}
	} else if m := reByName.FindStringSubmatch(base); m != nil {
		title = strings.TrimSpace(m[1])
		author = strings.TrimSpace(m[2])
	} else if m := reDashAuthor.FindStringSubmatch(base); m != nil {
		// “书名 - 作者”形式，仅当后段较短且不含书名号时采用
		title = strings.TrimSpace(m[1])
		author = strings.TrimSpace(m[2])
	}

	if author == "" {
		head := text
		if r := []rune(head); len(r) > 4000 {
			head = string(r[:4000])
		}
		if m := reAuthorLine.FindStringSubmatch(head); m != nil {
			author = strings.TrimSpace(m[1])
		}
	}
	title = strings.TrimSpace(strings.Trim(title, "《》"))
	if title == "" {
		title = "未命名"
	}
	if utf8.RuneCountInString(title) > 80 {
		title = string([]rune(title)[:80])
	}
	return title, author
}

// chunk 在没有章节标题时按段落切成约 9000 字的片段。
func chunk(text string) []Chapter {
	total := utf8.RuneCountInString(text)
	if total == 0 {
		return nil
	}
	if total < 20000 {
		return []Chapter{{Title: "全文", Start: 0, End: len(text)}}
	}
	const target = 9000
	var chapters []Chapter
	start, part := 0, 1
	for start < len(text) {
		remain := utf8.RuneCountInString(text[start:])
		if remain <= target*3/2 {
			chapters = append(chapters, Chapter{Title: sectionTitle(part), Start: start, End: len(text)})
			break
		}
		approx := start + target*3
		if approx >= len(text) {
			approx = len(text) - 1
		}
		cut := strings.Index(text[approx:], "\n\n")
		if cut < 0 || cut > target*3 {
			cut = strings.Index(text[approx:], "\n")
		}
		var end int
		if cut >= 0 {
			end = approx + cut + 1
		} else {
			end = approx
		}
		// 对齐到 UTF-8 字符边界
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end++
		}
		if end <= start {
			end = len(text)
		}
		chapters = append(chapters, Chapter{Title: sectionTitle(part), Start: start, End: end})
		start = end
		part++
	}
	return chapters
}

func sectionTitle(n int) string {
	if n == 1 {
		return "正文"
	}
	return "正文 · " + strconv.Itoa(n)
}

// HTML 把纯文本段落转换为安全的 HTML。
func HTML(body string) string {
	var sb strings.Builder
	blocks := reParagraphs.Split(body, -1)
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if reSeparator.MatchString(block) {
			sb.WriteString("<p class=\"txt-sep\">· · ·</p>\n")
			continue
		}
		lines := strings.Split(block, "\n")
		for i := range lines {
			lines[i] = html.EscapeString(strings.TrimLeft(lines[i], " \t　"))
		}
		sb.WriteString("<p>")
		sb.WriteString(strings.Join(lines, "<br/>"))
		sb.WriteString("</p>\n")
	}
	return sb.String()
}
