package kindle

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// EXTH record types worth surfacing. The full set runs to several hundred
// vendor-specific keys; these are the ones that carry bibliographic data.
const (
	exthAuthor       = 100
	exthPublisher    = 101
	exthDescription  = 103
	exthISBN         = 104
	exthSubject      = 105
	exthPublished    = 106
	exthRights       = 109
	exthASIN         = 113
	exthCoverOffset  = 201
	exthThumbOffset  = 202
	exthUpdatedTitle = 503
	exthLanguage     = 524
)

// exthRecords holds the EXTH payloads keyed by type, kept as raw bytes because
// some types are text and others are big-endian integers. A type may
// legitimately repeat (multiple authors or subjects).
type exthRecords struct {
	values   map[uint32][][]byte
	codepage int
}

func parseEXTH(r0 []byte, start int, codepage int) exthRecords {
	e := exthRecords{values: map[uint32][][]byte{}, codepage: codepage}
	if start+12 > len(r0) || string(r0[start:start+4]) != "EXTH" {
		return e
	}
	count := int(be32(r0, start+8))
	pos := start + 12
	for i := 0; i < count; i++ {
		if pos+8 > len(r0) {
			break
		}
		typ := binary.BigEndian.Uint32(r0[pos : pos+4])
		size := int(binary.BigEndian.Uint32(r0[pos+4 : pos+8]))
		if size < 8 || pos+size > len(r0) {
			break
		}
		e.values[typ] = append(e.values[typ], r0[pos+8:pos+size])
		pos += size
	}
	return e
}

// first returns the first text value for a type, or "".
func (e exthRecords) first(typ uint32) string {
	v := e.values[typ]
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(decodeText(v[0], e.codepage))
}

// all returns every text value for a type with blanks removed.
func (e exthRecords) all(typ uint32) []string {
	var out []string
	for _, v := range e.values[typ] {
		if s := strings.TrimSpace(decodeText(v, e.codepage)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// number reads a 4-byte EXTH payload as a big-endian integer.
func (e exthRecords) number(typ uint32) (uint32, bool) {
	v := e.values[typ]
	if len(v) == 0 || len(v[0]) != 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(v[0]), true
}

// decodeText converts MOBI text bytes to a Go string using the book's declared
// code page. MOBI files are either UTF-8 (65001) or Windows-1252 (1252).
func decodeText(b []byte, codepage int) string {
	if codepage == 65001 && utf8.Valid(b) {
		return string(b)
	}
	// cp1252 never fails, which makes it a safe fallback for mislabelled files.
	s, err := charmap.Windows1252.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(s)
}
