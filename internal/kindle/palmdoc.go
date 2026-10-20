package kindle

// decompressPalmDoc expands one PalmDOC (LZ77) compressed text record.
//
// The encoding is byte-oriented:
//
//	0x00        literal NUL
//	0x01..0x08  copy the next N bytes literally
//	0x09..0x7f  literal byte
//	0x80..0xbf  two-byte back-reference: 11 bits of distance, 3 bits of length
//	0xc0..0xff  a space followed by the byte with bit 7 cleared
func decompressPalmDoc(in []byte) []byte {
	out := make([]byte, 0, len(in)*4)
	for i := 0; i < len(in); {
		c := in[i]
		i++
		switch {
		case c == 0x00 || (c >= 0x09 && c <= 0x7f):
			out = append(out, c)
		case c >= 0x01 && c <= 0x08:
			n := int(c)
			if i+n > len(in) {
				n = len(in) - i
			}
			out = append(out, in[i:i+n]...)
			i += n
		case c >= 0x80 && c <= 0xbf:
			if i >= len(in) {
				return out
			}
			pair := int(c)<<8 | int(in[i])
			i++
			distance := (pair >> 3) & 0x07ff
			length := (pair & 0x0007) + 3
			if distance == 0 || distance > len(out) {
				// Corrupt back-reference: skip it rather than abandoning the
				// rest of the record.
				continue
			}
			start := len(out) - distance
			for j := 0; j < length; j++ {
				out = append(out, out[start+j])
			}
		default: // 0xc0..0xff
			out = append(out, ' ', c^0x80)
		}
	}
	return out
}
