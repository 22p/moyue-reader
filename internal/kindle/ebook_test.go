package kindle

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestDecompressPalmDoc(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"literals", []byte("hello"), "hello"},
		{"space compression", []byte{'a', 0xe2, 'b'}, "a bb"},
		{"literal run", append([]byte{0x03}, "xyz"...), "xyz"},
		{
			// "abcabc": the second "abc" is a back-reference three bytes back.
			name: "back reference",
			in:   append([]byte("abc"), 0x80|(3<<3)>>8, byte(3<<3)),
			want: "abcabc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(decompressPalmDoc(tc.in)); got != tc.want {
				t.Errorf("decompressPalmDoc(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDecompressPalmDocIgnoresBadBackReference(t *testing.T) {
	// A back-reference pointing before the start of the output must not panic
	// or truncate everything after it.
	in := []byte{0x80 | 0x0f, 0xf8, 'o', 'k'}
	if got := string(decompressPalmDoc(in)); !strings.HasSuffix(got, "ok") {
		t.Errorf("decompressPalmDoc kept %q, want it to recover and end with \"ok\"", got)
	}
}

func TestTrimTrailingEntries(t *testing.T) {
	// One trailing entry of three bytes: the length byte encodes the whole
	// entry, high bit set to mark the end of the backwards varint.
	rec := append([]byte("text"), 0x01, 0x02, 0x83)
	if got := string(trimTrailingEntries(rec, 0x02)); got != "text" {
		t.Errorf("trimTrailingEntries with one trailer = %q, want \"text\"", got)
	}

	// Multibyte overlap: the final byte's low bits say how many bytes belong
	// to the next record.
	rec = append([]byte("text"), 'x', 'y', 0x02)
	if got := string(trimTrailingEntries(rec, 0x01)); got != "text" {
		t.Errorf("trimTrailingEntries with overlap = %q, want \"text\"", got)
	}

	if got := string(trimTrailingEntries([]byte("plain"), 0)); got != "plain" {
		t.Errorf("trimTrailingEntries with no flags = %q, want \"plain\"", got)
	}
}

func TestSniffImage(t *testing.T) {
	cases := map[string][]byte{
		"jpeg": {0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0},
		"png":  {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		"gif":  []byte("GIF89a12"),
		"bmp":  []byte("BM123456"),
		"":     []byte("FLIS0000"),
	}
	for want, data := range cases {
		if got := sniffImage(data); got != want {
			t.Errorf("sniffImage(%q) = %q, want %q", data[:4], got, want)
		}
	}
	if got := sniffImage([]byte{1, 2}); got != "" {
		t.Errorf("sniffImage of a short record = %q, want \"\"", got)
	}
}

func TestDecodeText(t *testing.T) {
	if got := decodeText([]byte("caf\xc3\xa9"), 65001); got != "café" {
		t.Errorf("UTF-8 text decoded as %q", got)
	}
	if got := decodeText([]byte("caf\xe9"), 1252); got != "café" {
		t.Errorf("cp1252 text decoded as %q", got)
	}
	// A file that claims UTF-8 but is not must still produce readable text.
	if got := decodeText([]byte("caf\xe9"), 65001); got != "café" {
		t.Errorf("mislabelled text decoded as %q", got)
	}
}

func TestParsePalmDBRejectsRubbish(t *testing.T) {
	if _, err := parsePalmDB([]byte("too short")); err == nil {
		t.Error("parsePalmDB accepted a file that is too small")
	}

	// A header that claims more records than the file can hold.
	data := make([]byte, 100)
	binary.BigEndian.PutUint16(data[76:], 500)
	if _, err := parsePalmDB(data); err == nil {
		t.Error("parsePalmDB accepted a truncated record index")
	}
}

func TestDecodeRejectsNonKindleFiles(t *testing.T) {
	data := make([]byte, 200)
	copy(data[60:64], "DATA")
	copy(data[64:68], "TEST")
	binary.BigEndian.PutUint16(data[76:], 1)
	binary.BigEndian.PutUint32(data[78:], 100)

	_, err := Decode(data)
	if err == nil {
		t.Fatal("Decode accepted a file that is not a Kindle book")
	}
	if !strings.Contains(err.Error(), "not a Kindle book") {
		t.Errorf("error was %q, want it to explain the file is not a Kindle book", err)
	}
}

// buildMinimalBook assembles an uncompressed MOBI 6 file in memory, which is
// enough to exercise the container, header and text paths end to end.
func buildMinimalBook(t *testing.T, text string) []byte {
	t.Helper()

	title := "Test Book"
	record0 := make([]byte, 0x100)
	binary.BigEndian.PutUint16(record0[0x00:], compressionNone)
	binary.BigEndian.PutUint32(record0[0x04:], uint32(len(text)))
	binary.BigEndian.PutUint16(record0[0x08:], 1)
	binary.BigEndian.PutUint16(record0[0x0a:], 4096)
	copy(record0[0x10:], "MOBI")
	binary.BigEndian.PutUint32(record0[0x14:], 0xe8) // header length
	binary.BigEndian.PutUint32(record0[0x18:], 2)    // book
	binary.BigEndian.PutUint32(record0[0x1c:], 65001)
	binary.BigEndian.PutUint32(record0[0x24:], 6) // MOBI 6
	binary.BigEndian.PutUint32(record0[0x54:], 0xc0)
	binary.BigEndian.PutUint32(record0[0x58:], uint32(len(title)))
	copy(record0[0xc0:], title)

	records := [][]byte{record0, []byte(text)}

	var buf bytes.Buffer
	header := make([]byte, palmHeaderSize)
	copy(header[0:], "test")
	copy(header[60:64], "BOOK")
	copy(header[64:68], "MOBI")
	binary.BigEndian.PutUint16(header[76:], uint16(len(records)))
	buf.Write(header)

	offset := palmHeaderSize + len(records)*8
	index := make([]byte, 0, len(records)*8)
	for _, rec := range records {
		entry := make([]byte, 8)
		binary.BigEndian.PutUint32(entry, uint32(offset))
		index = append(index, entry...)
		offset += len(rec)
	}
	buf.Write(index)
	for _, rec := range records {
		buf.Write(rec)
	}
	return buf.Bytes()
}

func TestDecodeMinimalBook(t *testing.T) {
	const markup = "<html><body><p>Hello, reader.</p></body></html>"
	book, err := Decode(buildMinimalBook(t, markup))
	if err != nil {
		t.Fatalf("Decode returned %v", err)
	}
	if book.Title != "Test Book" {
		t.Errorf("title is %q, want \"Test Book\"", book.Title)
	}
	if book.HTML != markup {
		t.Errorf("markup is %q, want %q", book.HTML, markup)
	}
	if book.Compression != "none" {
		t.Errorf("compression reported as %q", book.Compression)
	}
	if book.AuthorLine() != "Unknown author" {
		t.Errorf("author line is %q for a book with no author", book.AuthorLine())
	}
}
