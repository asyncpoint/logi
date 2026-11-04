package parser

import (
	"strings"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
)

// Generic is the fallback parser: it recognises a leading timestamp and a
// severity token among the first few tokens, and leaves the rest as message.
type Generic struct {
	// Ref is when the log was written (e.g. the file mtime). It resolves the
	// year of year-less timestamps; zero means "now".
	Ref time.Time
}

func (Generic) Name() string { return "generic" }

func isTokSep(c byte) bool { return c == ' ' || c == '\t' || c == '|' }

// findSeverity looks at up to three tokens of line starting at pos.
func findSeverity(line string, pos int) (engine.Severity, int) {
	i := pos
	for tok := 0; tok < 3 && i < len(line); tok++ {
		for i < len(line) && isTokSep(line[i]) {
			i++
		}
		start := i
		for i < len(line) && !isTokSep(line[i]) {
			i++
		}
		if start == i {
			break
		}
		raw := line[start:i]
		t := strings.Trim(raw, "[]()<>:,;-")
		if len(t) == 1 && len(raw) > 1 && strings.ContainsAny(raw[:1]+raw[len(raw)-1:], "[(<") {
			if s := engine.ParseSeverityLetter(t[0]); s != engine.SevUnknown { // "[E]" "(W)"
				return s, i
			}
		}
		if eq := strings.IndexByte(t, '='); eq > 0 {
			switch strings.ToLower(t[:eq]) {
			case "level", "lvl", "severity", "sev":
				if s := engine.ParseSeverity(strings.Trim(t[eq+1:], `"'`)); s != engine.SevUnknown {
					return s, i
				}
			}
			continue
		}
		if s := engine.ParseSeverity(t); s != engine.SevUnknown {
			return s, i
		}
	}
	return engine.SevUnknown, pos
}

func skipSeparators(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '-', ':', '|', ']', '>', ',':
			i++
		default:
			return i
		}
	}
	return i
}

func (g Generic) Parse(text string) (engine.LogRecord, error) {
	rec := engine.LogRecord{Message: text}
	first := text
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		first = text[:nl]
		rec.Multiline = true
	}
	pos := 0
	if sev, ts, n, ok := parseGlog(first, g.Ref); ok { // E0920 11:32:04.123456 1234 file.go:12] msg
		rec.Severity, rec.Timestamp = sev, engine.TimestampOf(ts)
		rec.Message = strings.TrimLeft(first[n:], " ") + text[len(first):]
		return rec, nil
	}
	if t, n, ok := parseTimestampPrefixRef(first, g.Ref); ok {
		rec.Timestamp, pos = engine.TimestampOf(t), n
	}
	if sev, end := findSeverity(first, pos); sev != engine.SevUnknown {
		rec.Severity, pos = sev, end
	}
	if pos > 0 {
		pos = skipSeparators(text, pos)
		rec.Message = text[pos:]
	}
	return rec, nil
}

// parseGlog recognises Google glog/klog headers: "Lmmdd hh:mm:ss.uuuuuu ...".
func parseGlog(line string, ref time.Time) (engine.Severity, time.Time, int, bool) {
	if len(line) < 21 || !isDig(line[1]) || !isDig(line[4]) || line[5] != ' ' {
		return 0, time.Time{}, 0, false
	}
	switch line[0] {
	case 'I', 'W', 'E', 'F':
	default:
		return 0, time.Time{}, 0, false
	}
	mon, ok1 := atoi(line, 1, 2)
	day, ok2 := atoi(line, 3, 2)
	if !ok1 || !ok2 || mon < 1 || mon > 12 || day < 1 || day > 31 {
		return 0, time.Time{}, 0, false
	}
	hh, mm, ss, ns, _, next, ok := hms(line, 6)
	if !ok {
		return 0, time.Time{}, 0, false
	}
	ts := time.Date(inferYear(ref, mon, day), time.Month(mon), day, hh, mm, ss, ns, time.UTC)
	return engine.ParseSeverityLetter(line[0]), ts, next, true
}
