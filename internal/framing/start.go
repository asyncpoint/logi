package framing

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func digits(b []byte, from, n int) bool {
	if from+n > len(b) {
		return false
	}
	for i := from; i < from+n; i++ {
		if !isDigit(b[i]) {
			return false
		}
	}
	return true
}

func isSep(c byte) bool { return c == '-' || c == '/' || c == '.' }

var months = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// LooksLikeTimestamp is a cheap, allocation-free test for "this physical line
// starts with a timestamp", covering ISO-8601, dd-mm-yyyy, time-only and
// syslog forms, optionally wrapped in [ ] or ( ).
//
// Leading terminal escape sequences (colour and style codes, as in output piped
// from docker, kubectl or a coloured logger) are ignored: they are removed from
// the text before parsing, so they must not hide the start of a record either.
func LooksLikeTimestamp(l []byte) bool {
	l = skipEscapes(l)
	if len(l) > 0 && (l[0] == '[' || l[0] == '(') {
		l = skipEscapes(l[1:])
	}
	switch {
	case digits(l, 0, 4) && len(l) > 9 && isSep(l[4]) && digits(l, 5, 2) && isSep(l[7]) && digits(l, 8, 2):
		return true // 2026-09-19 / 2026/09/19
	case digits(l, 0, 2) && len(l) > 9 && isSep(l[2]) && digits(l, 3, 2) && isSep(l[5]) && digits(l, 6, 4):
		return true // 19-09-2026 / 09/19/2026
	case digits(l, 0, 2) && len(l) > 7 && l[2] == ':' && digits(l, 3, 2) && l[5] == ':' && digits(l, 6, 2):
		return true // 11:32:04
	case len(l) > 14 && l[3] == ' ':
		for _, m := range months {
			if string(l[:3]) == m {
				i := 4
				for i < len(l) && l[i] == ' ' {
					i++
				}
				return digits(l, i, 1) && (digits(l, i+1, 1) && l[i+2] == ' ' || l[i+1] == ' ') // "Sep 19 " / "Sep  9 "
			}
		}
	}
	return false
}

// skipEscapes returns l without any escape sequences at its start. It only
// reslices, so the common case (no ESC) costs one byte comparison.
func skipEscapes(l []byte) []byte {
	for n := 0; n < 8 && len(l) >= 2 && l[0] == 0x1b; n++ {
		j := 2
		if l[1] == '[' { // CSI: parameters, intermediates, then one final byte
			for j < len(l) && l[j] >= 0x30 && l[j] <= 0x3f {
				j++
			}
			for j < len(l) && l[j] >= 0x20 && l[j] <= 0x2f {
				j++
			}
			if j < len(l) && l[j] >= 0x40 && l[j] <= 0x7e {
				j++
			}
		}
		l = l[j:]
	}
	return l
}
