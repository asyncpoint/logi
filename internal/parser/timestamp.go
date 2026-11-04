package parser

import (
	"sync"
	"time"
)

// inferYear picks the year for a timestamp that has none (syslog, glog). ref is
// the best known "when was this written" time (typically the file's mtime; zero
// means now). A date more than a month after ref cannot have been written yet,
// so it belongs to the previous year (December lines read in January).
func inferYear(ref time.Time, mon, day int) int {
	if ref.IsZero() {
		ref = time.Now()
	}
	y := ref.Year()
	if time.Date(y, time.Month(mon), day, 0, 0, 0, 0, time.UTC).After(ref.AddDate(0, 0, 31)) {
		y--
	}
	return y
}

// fixedZones caches zones for non-whole-hour offsets; time.FixedZone allocates
// on every call and every parsed record would otherwise retain its own *Location.
var fixedZones sync.Map // int offset seconds -> *time.Location

func zoneFor(offset int) *time.Location {
	if v, ok := fixedZones.Load(offset); ok {
		return v.(*time.Location)
	}
	l := time.FixedZone("", offset)
	if v, loaded := fixedZones.LoadOrStore(offset, l); loaded {
		return v.(*time.Location)
	}
	return l
}

func isDig(c byte) bool { return c >= '0' && c <= '9' }

func atoi(s string, i, n int) (int, bool) {
	if i+n > len(s) {
		return 0, false
	}
	v := 0
	for k := i; k < i+n; k++ {
		if !isDig(s[k]) {
			return 0, false
		}
		v = v*10 + int(s[k]-'0')
	}
	return v, true
}

func isDateSep(c byte) bool { return c == '-' || c == '/' || c == '.' }

var monthNames = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// hms parses HH:MM:SS[.,fraction][zone] at s[i:].
func hms(s string, i int) (hh, mm, ss, nsec int, loc *time.Location, next int, ok bool) {
	var ok1, ok2, ok3 bool
	if hh, ok1 = atoi(s, i, 2); !ok1 || i+8 > len(s) || s[i+2] != ':' || s[i+5] != ':' {
		return
	}
	mm, ok2 = atoi(s, i+3, 2)
	ss, ok3 = atoi(s, i+6, 2)
	if !ok2 || !ok3 || hh > 23 || mm > 59 || ss > 60 {
		return
	}
	next = i + 8
	loc = time.UTC
	if next < len(s) && (s[next] == '.' || s[next] == ',') && next+1 < len(s) && isDig(s[next+1]) {
		j := next + 1
		scale := 100000000
		for j < len(s) && isDig(s[j]) {
			if scale > 0 {
				nsec += int(s[j]-'0') * scale
				scale /= 10
			}
			j++
		}
		next = j
	} else if next+5 <= len(s) && s[next] == ',' && s[next+1] == ' ' && isDig(s[next+2]) && isDig(s[next+3]) &&
		isDig(s[next+4]) && (next+5 == len(s) || !isDig(s[next+5])) {
		// ", 110": comma, space, exactly three digits (NCache event logs). Anything else,
		// such as "12:00:00, 5 files failed", is not a fraction.
		nsec = ((int(s[next+2]-'0')*10+int(s[next+3]-'0'))*10 + int(s[next+4]-'0')) * 1_000_000
		next += 5
	}
	// optional zone: Z, ±hh:mm, ±hhmm, ±hh (optionally after one space)
	z := next
	if z < len(s) && s[z] == ' ' && z+1 < len(s) && (s[z+1] == '+' || s[z+1] == '-') {
		z++
	}
	switch {
	case z < len(s) && s[z] == 'Z':
		next = z + 1
	case z < len(s) && (s[z] == '+' || s[z] == '-'):
		sign := 1
		if s[z] == '-' {
			sign = -1
		}
		oh, ok := atoi(s, z+1, 2)
		if !ok {
			break
		}
		om, end := 0, z+3
		if end < len(s) && s[end] == ':' {
			if v, ok := atoi(s, end+1, 2); ok {
				om, end = v, end+3
			}
		} else if v, ok := atoi(s, end, 2); ok {
			om, end = v, end+2
		}
		if oh <= 14 && om <= 59 {
			loc = zoneFor(sign * (oh*3600 + om*60))
			next = end
		}
	}
	return hh, mm, ss, nsec, loc, next, true
}

// ParseTimestampPrefix parses a timestamp at the start of s (optionally wrapped
// in [] or ()) and returns it with the number of bytes consumed. Times are kept
// as written (wall clock); no conversion to local time is performed.
func ParseTimestampPrefix(s string) (time.Time, int, bool) {
	return parseTimestampPrefixRef(s, time.Time{})
}

func parseTimestampPrefixRef(s string, ref time.Time) (time.Time, int, bool) {
	i := 0
	var closer byte
	if len(s) > 0 && (s[0] == '[' || s[0] == '(') {
		closer = ']'
		if s[0] == '(' {
			closer = ')'
		}
		i = 1
	}
	var year, mon, day int
	loc := time.UTC
	var hh, mm, ss, ns int
	haveTime := false

	switch {
	case len(s) >= i+10 && isDateSep(s[i+4]) && isDateSep(s[i+7]):
		var ok1, ok2, ok3 bool
		year, ok1 = atoi(s, i, 4)
		mon, ok2 = atoi(s, i+5, 2)
		day, ok3 = atoi(s, i+8, 2)
		if !ok1 || !ok2 || !ok3 {
			return time.Time{}, 0, false
		}
		i += 10
	case len(s) >= i+10 && isDateSep(s[i+2]) && isDateSep(s[i+5]) && isDig(s[i+6]):
		a, ok1 := atoi(s, i, 2)
		b, ok2 := atoi(s, i+3, 2)
		y, ok3 := atoi(s, i+6, 4)
		if !ok1 || !ok2 || !ok3 {
			return time.Time{}, 0, false
		}
		year = y
		if s[i+2] == '/' { // US style mm/dd/yyyy
			mon, day = a, b
		} else { // dd-mm-yyyy, dd.mm.yyyy
			day, mon = a, b
		}
		i += 10
	case len(s) >= i+8 && s[i+2] == ':' && s[i+5] == ':':
		h, m, sec, n, l, next, ok := hms(s, i)
		if !ok {
			return time.Time{}, 0, false
		}
		year, mon, day = 1, 1, 1
		hh, mm, ss, ns, loc, haveTime, i = h, m, sec, n, l, true, next
	case len(s) >= i+15 && s[i+3] == ' ':
		m := 0
		for k, name := range monthNames {
			if s[i:i+3] == name {
				m = k + 1
				break
			}
		}
		if m == 0 {
			return time.Time{}, 0, false
		}
		j := i + 4
		for j < len(s) && s[j] == ' ' {
			j++
		}
		d, k := 0, j
		for k < len(s) && k < j+2 && isDig(s[k]) {
			d = d*10 + int(s[k]-'0')
			k++
		}
		if k == j || k >= len(s) || s[k] != ' ' {
			return time.Time{}, 0, false
		}
		h, mi, sec, n, l, next, ok := hms(s, k+1)
		if !ok {
			return time.Time{}, 0, false
		}
		year, mon, day, hh, mm, ss, ns, loc, haveTime, i = inferYear(ref, m, d), m, d, h, mi, sec, n, l, true, next
	default:
		return time.Time{}, 0, false
	}
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		return time.Time{}, 0, false
	}
	if !haveTime && i < len(s) && (s[i] == 'T' || s[i] == ' ') {
		if h, m, sec, n, l, next, ok := hms(s, i+1); ok {
			hh, mm, ss, ns, loc, i = h, m, sec, n, l, next
		}
	}
	if closer != 0 && i < len(s) && s[i] == closer {
		i++
	}
	return time.Date(year, time.Month(mon), day, hh, mm, ss, ns, loc), i, true
}
