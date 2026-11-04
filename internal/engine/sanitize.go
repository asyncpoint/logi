package engine

import (
	"strings"
	"unicode/utf8"
)

// unsafeRune reports runes that must never reach a terminal: C0 controls, DEL,
// C1 controls (U+0080–U+009F, e.g. U+009B is an 8-bit CSI on some terminals),
// line/paragraph separators and bidirectional overrides/isolates that can
// visually reorder text. Zero-width joiners are kept because emoji need them.
func unsafeRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, r == 0x2028, r == 0x2029,
		r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0xfeff:
		return true
	}
	return false
}

// SanitizeText makes untrusted text (log content, file names) safe to print.
// Unsafe runes become a space and invalid UTF-8 bytes become '?', so the result
// is always valid UTF-8. Clean input is returned unchanged without allocating.
func SanitizeText(s string) string {
	s = StripANSI(s) // drop whole sequences, not just the ESC byte, so no "[32m" debris is left
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf { // ASCII fast path: only C0 controls and DEL are unsafe
			if c < 0x20 || c == 0x7f {
				return sanitizeSlow(s, i)
			}
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && n == 1) || unsafeRune(r) {
			return sanitizeSlow(s, i)
		}
		i += n
	}
	return s
}

func sanitizeSlow(s string, i int) string {
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:i])
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b.WriteByte('?')
		case unsafeRune(r):
			b.WriteByte(' ')
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// SanitizeKeepTabs is SanitizeText for multi-line views. Line breaks are kept
// (a stack trace must stay a stack trace), each line loses a trailing CR (so CRLF
// text breaks cleanly) and its tabs become four spaces; every other unsafe rune is
// handled line by line as in SanitizeText. Keeping the breaks is safe because the
// caller renders each line on its own row.
func SanitizeKeepTabs(s string) string {
	if strings.IndexByte(s, '\n') < 0 {
		return SanitizeText(strings.ReplaceAll(strings.TrimRight(s, "\r"), "\t", "    "))
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = SanitizeText(strings.ReplaceAll(strings.TrimRight(l, "\r"), "\t", "    "))
	}
	return strings.Join(lines, "\n")
}
