package engine

import "strings"

// StripANSI removes terminal escape sequences from s: colour and style codes
// (CSI … m), cursor and erase codes, OSC strings (window titles, hyperlinks) and
// the two-character escapes. Container logs are full of them (Spring Boot, Node
// and pytest colour their output), and left in place they hide timestamps and
// levels from the parsers and show up as "[32m" noise. Text without an ESC byte
// is returned unchanged, without allocating.
func StripANSI(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	run := 0 // start of the pending run of text that is kept as is
	for i := 0; i < len(s); {
		// Copy whole spans between escapes (IndexByte is vectorised) rather than
		// one byte at a time.
		k := strings.IndexByte(s[i:], 0x1b)
		if k < 0 {
			break
		}
		i += k
		b.WriteString(s[run:i])
		if i+1 >= len(s) { // a lone ESC at the very end
			return b.String()
		}
		switch s[i+1] {
		case '[': // CSI: parameters 0x30-0x3F, intermediates 0x20-0x2F, one final byte 0x40-0x7E
			j := i + 2
			for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
				j++
			}
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
				j++
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
				j++
			}
			i = j
		case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: a string ended by BEL or ESC \
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					j++
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		default: // ESC, optional intermediates (ESC ( B), one final byte
			j := i + 1
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
				j++
			}
			if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7e {
				j++
			}
			i = j
		}
		run = i
	}
	b.WriteString(s[run:])
	return b.String()
}
