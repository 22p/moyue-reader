package kindle

import "fmt"

// decompressText expands every text record into one contiguous byte slice.
func decompressText(db *palmDB, h *mobiHeader) ([]byte, error) {
	if h.encryption != 0 {
		return nil, fmt.Errorf("the book is DRM protected (encryption type %d); remove the DRM first", h.encryption)
	}

	var huff *huffReader
	if h.compression == compressionHuffCdic {
		var err error
		huff, err = loadHuffTables(db, h)
		if err != nil {
			return nil, err
		}
	}

	out := make([]byte, 0, h.textLength)
	last := h.lastTextRecord()
	if last > db.count() {
		last = db.count()
	}
	for i := firstTextRecord; i < last; i++ {
		rec := db.record(i)
		if rec == nil {
			continue
		}
		rec = trimTrailingEntries(rec, h.extraDataFlags)

		switch h.compression {
		case compressionNone:
			out = append(out, rec...)
		case compressionPalmDoc:
			out = append(out, decompressPalmDoc(rec)...)
		case compressionHuffCdic:
			chunk, err := huff.decompress(rec)
			if err != nil {
				return nil, fmt.Errorf("text record %d: %w", i, err)
			}
			out = append(out, chunk...)
		default:
			return nil, fmt.Errorf("unsupported compression type %d", h.compression)
		}
	}

	// Note that the stream can legitimately run past textLength: in KF8 that
	// field measures the main flow only, and the stylesheet flows follow it.
	if len(out) == 0 {
		return nil, fmt.Errorf("the book contains no readable text records")
	}
	return out, nil
}

func loadHuffTables(db *palmDB, h *mobiHeader) (*huffReader, error) {
	if h.huffOffset <= 0 || h.huffCount < 1 {
		return nil, fmt.Errorf("the book uses HUFF/CDIC compression but declares no HUFF records")
	}
	huff := db.record(h.huffOffset)
	if huff == nil {
		return nil, fmt.Errorf("HUFF record %d is missing", h.huffOffset)
	}
	var cdics [][]byte
	for i := 1; i < h.huffCount; i++ {
		rec := db.record(h.huffOffset + i)
		if rec == nil {
			break
		}
		cdics = append(cdics, rec)
	}
	return newHuffReader(huff, cdics)
}

// trimTrailingEntries strips the multibyte-overlap and index data that Kindle
// appends to each text record. The extra-data flags say which trailers are
// present; each is length-prefixed backwards from the end of the record.
func trimTrailingEntries(rec []byte, flags uint16) []byte {
	size := len(rec)
	num := 0

	for f := flags >> 1; f != 0; f >>= 1 {
		if f&1 != 0 {
			num += trailingEntrySize(rec, size-num)
		}
	}
	if flags&1 != 0 {
		if idx := size - num - 1; idx >= 0 && idx < len(rec) {
			num += int(rec[idx]&0x3) + 1
		}
	}

	if num >= size {
		return nil
	}
	return rec[:size-num]
}

// trailingEntrySize reads the backwards varint that gives a trailer's length.
func trailingEntrySize(rec []byte, end int) int {
	if end <= 0 || end > len(rec) {
		return 0
	}
	bitpos, result := 0, 0
	for {
		v := rec[end-1]
		result |= int(v&0x7f) << bitpos
		bitpos += 7
		end--
		if v&0x80 != 0 || bitpos >= 28 || end == 0 {
			return result
		}
	}
}
