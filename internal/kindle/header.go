package kindle

import (
	"fmt"
	"strings"
)

// Compression identifiers found in the PalmDOC header.
const (
	compressionNone     = 1
	compressionPalmDoc  = 2
	compressionHuffCdic = 17480
)

// mobiHeader holds the fields of record 0 that matter for extraction. All
// offsets are relative to the start of record 0, which is how the PalmDOC
// header (16 bytes) and the MOBI header that follows it share one address
// space.
type mobiHeader struct {
	compression  int
	textLength   int
	textRecords  int
	encryption   int
	headerLength int
	mobiType     int
	codepage     int
	version      int

	firstNonText  int
	title         string
	locale        int
	firstResource int

	huffOffset int
	huffCount  int

	exthFlags      uint32
	extraDataFlags uint16

	// KF8-only section pointers.
	fdstRecord    int
	fdstFlowCount int
	ncxIndex      int
	fragmentIndex int
	skeletonIndex int
	guideIndex    int

	exth exthRecords
}

func parseMobiHeader(r0 []byte) (*mobiHeader, error) {
	if len(r0) < 20 {
		return nil, fmt.Errorf("record 0 is too small (%d bytes)", len(r0))
	}
	if string(r0[16:20]) != "MOBI" {
		return nil, fmt.Errorf("record 0 is not a MOBI header (found %q)", printable(r0[16:20]))
	}

	h := &mobiHeader{
		compression:  int(be16(r0, 0x00)),
		textLength:   int(be32(r0, 0x04)),
		textRecords:  int(be16(r0, 0x08)),
		encryption:   int(be16(r0, 0x0c)),
		headerLength: int(be32(r0, 0x14)),
		mobiType:     int(be32(r0, 0x18)),
		codepage:     int(be32(r0, 0x1c)),
		version:      int(be32(r0, 0x24)),

		firstNonText:  int(be32(r0, 0x50)),
		locale:        int(be32(r0, 0x5c)),
		firstResource: int(be32(r0, 0x6c)),

		huffOffset: int(be32(r0, 0x70)),
		huffCount:  int(be32(r0, 0x74)),
		exthFlags:  be32(r0, 0x80),
	}

	titleOff := int(be32(r0, 0x54))
	titleLen := int(be32(r0, 0x58))
	if titleOff > 0 && titleLen > 0 && titleOff+titleLen <= len(r0) {
		h.title = decodeText(r0[titleOff:titleOff+titleLen], h.codepage)
	}

	// The trailing-entry flags only exist in headers long enough to hold them.
	if h.headerLength >= 0xe4 {
		h.extraDataFlags = be16(r0, 0xf2)
	}

	if h.version >= 8 {
		// KF8 reuses the record-number slots that MOBI 6 spends on the
		// first/last content record pair.
		h.fdstRecord = int(be32(r0, 0xc0))
		h.fdstFlowCount = int(be32(r0, 0xc4))
		h.ncxIndex = int(be32(r0, 0xf4))
		h.fragmentIndex = int(be32(r0, 0xf8))
		h.skeletonIndex = int(be32(r0, 0xfc))
		h.guideIndex = int(be32(r0, 0x104))
	} else {
		h.ncxIndex = int(be32(r0, 0xf4))
	}

	if h.exthFlags&0x40 != 0 {
		start := 16 + h.headerLength
		h.exth = parseEXTH(r0, start, h.codepage)
	}

	return h, nil
}

// firstTextRecord is always 1: record 0 holds the headers.
const firstTextRecord = 1

// lastTextRecord returns the index just past the final text record.
func (h *mobiHeader) lastTextRecord() int {
	return firstTextRecord + h.textRecords
}

func (h *mobiHeader) isKF8() bool { return h.version >= 8 }

func printable(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}
