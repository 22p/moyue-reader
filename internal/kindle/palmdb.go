package kindle

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// palmDB is a parsed Palm Database container, the outer wrapper used by every
// MOBI-family file (.mobi, .azw, .azw3, .prc).
type palmDB struct {
	name    string
	typ     string
	creator string
	records [][]byte
}

const palmHeaderSize = 78

// parsePalmDB splits raw file bytes into the container's records.
func parsePalmDB(data []byte) (*palmDB, error) {
	if len(data) < palmHeaderSize {
		return nil, fmt.Errorf("file is too small to be a MOBI container (%d bytes)", len(data))
	}

	db := &palmDB{
		name:    strings.TrimRight(string(data[0:32]), "\x00"),
		typ:     string(data[60:64]),
		creator: string(data[64:68]),
	}

	count := int(binary.BigEndian.Uint16(data[76:78]))
	if count == 0 {
		return nil, fmt.Errorf("container holds no records")
	}
	if palmHeaderSize+count*8 > len(data) {
		return nil, fmt.Errorf("record index is truncated (claims %d records)", count)
	}

	offsets := make([]int, 0, count+1)
	for i := 0; i < count; i++ {
		off := int(binary.BigEndian.Uint32(data[palmHeaderSize+i*8:]))
		if off < 0 || off > len(data) {
			return nil, fmt.Errorf("record %d has an out-of-range offset (%d)", i, off)
		}
		offsets = append(offsets, off)
	}
	offsets = append(offsets, len(data))

	db.records = make([][]byte, count)
	for i := 0; i < count; i++ {
		end := offsets[i+1]
		if end < offsets[i] {
			end = offsets[i]
		}
		db.records[i] = data[offsets[i]:end]
	}
	return db, nil
}

// record returns record i, or nil when i is out of range. Callers treat a nil
// record as "absent" rather than as an error, since optional MOBI sections are
// routinely addressed by index values that point past the end of the file.
func (p *palmDB) record(i int) []byte {
	if i < 0 || i >= len(p.records) {
		return nil
	}
	return p.records[i]
}

func (p *palmDB) count() int { return len(p.records) }

func be32(b []byte, off int) uint32 {
	if off < 0 || off+4 > len(b) {
		return 0
	}
	return binary.BigEndian.Uint32(b[off : off+4])
}

func be16(b []byte, off int) uint16 {
	if off < 0 || off+2 > len(b) {
		return 0
	}
	return binary.BigEndian.Uint16(b[off : off+2])
}
