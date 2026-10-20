package kindle

import (
	"encoding/binary"
	"fmt"
)

// huffReader decodes the HUFF/CDIC compression used by most modern KF8 books.
//
// A HUFF record carries two lookup tables: a 256-entry table indexed by the top
// byte of the next 32 bits, and a 64-entry table of per-length code ranges used
// when the first table cannot resolve the symbol on its own. Each CDIC record
// then contributes a slab of dictionary phrases; a decoded symbol is an index
// into the concatenated dictionary. Phrases may themselves be compressed, in
// which case they are expanded recursively and cached in place.
// Code ranges are held as 64-bit values: the reference algorithm shifts them
// left by up to 32 bits and compares against a 32-bit code, so a value that
// overflows uint32 must stay above every possible code rather than wrap.
type huffReader struct {
	codeLen [256]uint32
	term    [256]bool
	maxCode [256]uint64

	minCodes [33]uint64
	maxCodes [33]uint64

	phrases []huffPhrase
}

type huffPhrase struct {
	data     []byte
	expanded bool
}

// maxHuffDepth bounds the recursive expansion of dictionary phrases. Well
// formed files nest only a level or two; anything deeper is corruption.
const maxHuffDepth = 32

func newHuffReader(huff []byte, cdics [][]byte) (*huffReader, error) {
	if len(huff) < 24 || string(huff[0:4]) != "HUFF" {
		return nil, fmt.Errorf("HUFF record is missing or malformed")
	}
	off1 := int(be32(huff, 8))
	off2 := int(be32(huff, 12))
	if off1+256*4 > len(huff) || off2+64*4 > len(huff) {
		return nil, fmt.Errorf("HUFF record tables are out of range")
	}

	r := &huffReader{}
	for i := 0; i < 256; i++ {
		v := be32(huff, off1+i*4)
		codeLen := v & 0x1f
		if codeLen == 0 {
			return nil, fmt.Errorf("HUFF table entry %d has a zero code length", i)
		}
		r.codeLen[i] = codeLen
		r.term[i] = v&0x80 != 0
		r.maxCode[i] = (uint64(v>>8)+1)<<(32-codeLen) - 1
	}
	// The 64-entry second table holds 32 (min, max) pairs, where pair i covers
	// codes of length i+1. Slot 0 stays empty so that the arrays can be
	// indexed directly by code length.
	for i := 1; i <= 32; i++ {
		minCode := be32(huff, off2+(i-1)*8)
		maxCode := be32(huff, off2+(i-1)*8+4)
		shift := uint(32 - i)
		r.minCodes[i] = uint64(minCode) << shift
		r.maxCodes[i] = (uint64(maxCode)+1)<<shift - 1
	}

	for _, cdic := range cdics {
		if err := r.loadCDIC(cdic); err != nil {
			return nil, err
		}
	}
	if len(r.phrases) == 0 {
		return nil, fmt.Errorf("no CDIC dictionary entries were found")
	}
	return r, nil
}

func (r *huffReader) loadCDIC(cdic []byte) error {
	if len(cdic) < 16 || string(cdic[0:4]) != "CDIC" {
		return fmt.Errorf("CDIC record is missing or malformed")
	}
	total := int(be32(cdic, 8))
	bits := int(be32(cdic, 12))
	if bits > 16 {
		return fmt.Errorf("CDIC record declares an implausible index width (%d bits)", bits)
	}
	n := 1 << uint(bits)
	if remaining := total - len(r.phrases); n > remaining {
		n = remaining
	}
	if n < 0 {
		n = 0
	}
	for i := 0; i < n; i++ {
		if 16+i*2+2 > len(cdic) {
			break
		}
		off := int(be16(cdic, 16+i*2))
		if 16+off+2 > len(cdic) {
			break
		}
		blen := int(be16(cdic, 16+off))
		size := blen & 0x7fff
		start := 18 + off
		if start+size > len(cdic) {
			break
		}
		r.phrases = append(r.phrases, huffPhrase{
			data:     cdic[start : start+size],
			expanded: blen&0x8000 != 0,
		})
	}
	return nil
}

// decompress expands one HUFF/CDIC compressed record.
func (r *huffReader) decompress(data []byte) ([]byte, error) {
	return r.unpack(data, 0)
}

func (r *huffReader) unpack(data []byte, depth int) ([]byte, error) {
	if depth > maxHuffDepth {
		return nil, fmt.Errorf("dictionary phrases are nested too deeply (corrupt file?)")
	}

	bitsLeft := len(data) * 8
	// Eight bytes of slack let the 64-bit window read past the final byte.
	padded := make([]byte, len(data)+8)
	copy(padded, data)

	out := make([]byte, 0, len(data)*4)
	pos := 0
	x := binary.BigEndian.Uint64(padded[pos:])
	n := 32

	for {
		if n <= 0 {
			pos += 4
			if pos+8 > len(padded) {
				break
			}
			x = binary.BigEndian.Uint64(padded[pos:])
			n += 32
		}
		code := uint32(x >> uint(n))

		idx := code >> 24
		codeLen := r.codeLen[idx]
		maxCode := r.maxCode[idx]
		if !r.term[idx] {
			for codeLen < 32 && uint64(code) < r.minCodes[codeLen] {
				codeLen++
			}
			maxCode = r.maxCodes[codeLen]
		}

		n -= int(codeLen)
		bitsLeft -= int(codeLen)
		if bitsLeft < 0 {
			break
		}

		if maxCode < uint64(code) {
			return nil, fmt.Errorf("huffman code %#08x falls outside every code range", code)
		}
		i := int((maxCode - uint64(code)) >> (32 - codeLen))
		if i < 0 || i >= len(r.phrases) {
			return nil, fmt.Errorf("dictionary index %d is out of range", i)
		}
		p := r.phrases[i]
		if !p.expanded {
			expanded, err := r.unpack(p.data, depth+1)
			if err != nil {
				return nil, err
			}
			p = huffPhrase{data: expanded, expanded: true}
			r.phrases[i] = p
		}
		out = append(out, p.data...)
	}
	return out, nil
}
