package api

import (
	"html"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"moyue/internal/epub"
)

type searchHit struct {
	Chapter      int     `json:"chapter"`
	ChapterTitle string  `json:"chapterTitle"`
	Snippet      string  `json:"snippet"`
	Percent      float64 `json:"percent"`
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>`)
	reBlockBreak  = regexp.MustCompile(`(?is)<\s*(?:br|/p|/div|/li|/h[1-6]|/tr|/blockquote|/section)\s*/?>`)
	reAnyTag      = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpaces      = regexp.MustCompile(`\s+`)
)

// htmlToText 把章节 HTML 粗略转成纯文本，仅用于搜索与摘要。
func htmlToText(s string) string {
	s = reScriptStyle.ReplaceAllString(s, " ")
	s = reBlockBreak.ReplaceAllString(s, " ")
	s = reAnyTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
}

const (
	maxSearchHits     = 80
	maxSearchChapters = 600
	maxSearchBytes    = 64 << 20
)

func (s *Server) searchBook(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) < 1 || utf8.RuneCountInString(q) > 60 {
		writeError(w, http.StatusBadRequest, "搜索词需为 1-60 个字符")
		return
	}

	results := make([]searchHit, 0, 16)
	truncated := false
	scannedBytes := 0
	addChapter := func(index int, title, text string) bool {
		if len(results) >= maxSearchHits {
			truncated = true
			return false
		}
		scannedBytes += len(text)
		if scannedBytes > maxSearchBytes {
			truncated = true
			return false
		}
		hits := searchText(searchChapter{Index: index, Title: title, Text: text}, q, 3)
		if room := maxSearchHits - len(results); len(hits) > room {
			hits = hits[:room]
			truncated = true
		}
		results = append(results, hits...)
		if len(results) >= maxSearchHits {
			truncated = true
			return false
		}
		return true
	}

	switch b.Type {
	case "txt":
		tb, err := s.parseTXT(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "TXT 解析失败")
			return
		}
		for i, ch := range tb.Chapters {
			if i >= maxSearchChapters {
				truncated = true
				break
			}
			start, end := ch.Start, ch.End
			if start < 0 || end > len(tb.Text) || start >= end {
				continue
			}
			if !addChapter(i, ch.Title, tb.Text[start:end]) {
				break
			}
		}
	case "fb2":
		fb, err := s.parseFB2(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "FB2 解析失败")
			return
		}
		for i, ch := range fb.Chapters {
			if i >= maxSearchChapters {
				truncated = true
				break
			}
			if !addChapter(i, ch.Title, htmlToText(ch.HTML)) {
				break
			}
		}
	case "mobi", "azw", "azw3", "prc":
		_, list, err := s.parseKindle(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "MOBI/AZW3 解析失败")
			return
		}
		for i, ch := range list {
			if i >= maxSearchChapters {
				truncated = true
				break
			}
			if !addChapter(i, ch.Title, htmlToText(ch.HTML)) {
				break
			}
		}
	case "epub":
		eb, err := s.parseEPUB(b)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "EPUB 解析失败")
			return
		}
		ar, err := epub.OpenArchive(s.bookPath(b))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "无法打开电子书")
			return
		}
		defer ar.Close()
		for i, ch := range eb.Chapters {
			if i >= maxSearchChapters {
				truncated = true
				break
			}
			raw, err := ar.Read(ch.Href)
			if err != nil {
				continue
			}
			if !addChapter(i, ch.Title, htmlToText(string(raw))) {
				break
			}
		}
	default:
		writeError(w, http.StatusBadRequest, "该格式暂不支持全文搜索")
		return
	}

	if len(results) > maxSearchHits {
		results = results[:maxSearchHits]
		truncated = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "truncated": truncated})
}

type searchChapter struct {
	Index int
	Title string
	Text  string
}

func searchText(ch searchChapter, q string, max int) []searchHit {
	if ch.Text == "" || q == "" {
		return nil
	}
	runes := []rune(ch.Text)
	if len(runes) == 0 {
		return nil
	}
	lower := strings.ToLower(ch.Text)
	needle := strings.ToLower(q)
	if needle == "" {
		return nil
	}
	qRunes := utf8.RuneCountInString(q)
	out := make([]searchHit, 0, max)
	offset := 0
	for len(out) < max {
		idx := strings.Index(lower[offset:], needle)
		if idx < 0 {
			break
		}
		idx += offset
		runeStart := utf8.RuneCountInString(ch.Text[:idx])
		from := runeStart - 30
		if from < 0 {
			from = 0
		}
		to := runeStart + qRunes + 40
		if to > len(runes) {
			to = len(runes)
		}
		snippet := string(runes[from:to])
		if from > 0 {
			snippet = "…" + snippet
		}
		if to < len(runes) {
			snippet += "…"
		}
		out = append(out, searchHit{
			Chapter:      ch.Index,
			ChapterTitle: ch.Title,
			Snippet:      snippet,
			Percent:      float64(runeStart) / float64(len(runes)),
		})
		offset = idx + len(needle)
		if offset >= len(lower) {
			break
		}
	}
	return out
}
