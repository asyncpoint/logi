package parser

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/asyncpoint/logi/internal/engine"
)

// JSON parses one JSON object per record. Ingestion only extracts timestamp,
// level and message with an allocation-light top-level scan that stops as soon
// as all three are found. Full field access is via Fields().
type JSON struct{}

func (JSON) Name() string { return "json" }

var errNotJSON = errors.New("not a JSON object")

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	return i
}

// scanString returns the index just past the closing quote of the string
// starting at s[i]=='"', and whether it contained escapes.
func scanString(s string, i int) (end int, esc bool, ok bool) {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			esc = true
			j++
		case '"':
			return j + 1, esc, true
		}
	}
	return len(s), esc, false
}

// skipValue returns the index just past the JSON value starting at s[i].
func skipValue(s string, i int) (int, bool) {
	if i >= len(s) {
		return i, false
	}
	switch s[i] {
	case '"':
		e, _, ok := scanString(s, i)
		return e, ok
	case '{', '[':
		depth := 0
		for j := i; j < len(s); j++ {
			switch s[j] {
			case '"':
				e, _, ok := scanString(s, j)
				if !ok {
					return e, false
				}
				j = e - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, true
				}
			}
		}
		return len(s), false
	}
	j := i
	for j < len(s) && s[j] != ',' && s[j] != '}' && s[j] != ']' && s[j] != ' ' && s[j] != '\n' && s[j] != '\r' && s[j] != '\t' {
		j++
	}
	return j, j > i
}

func unquote(raw string, esc bool) string {
	inner := raw[1 : len(raw)-1]
	if !esc {
		return inner
	}
	if u, ok := jsonUnescape(inner); ok {
		return u
	}
	return inner
}

// jsonUnescape decodes the body of a JSON string (without quotes). It follows
// RFC 8259, unlike strconv.Unquote, which implements Go syntax and rejects \/
// and UTF-16 surrogate pairs. ok is false for malformed escapes.
func jsonUnescape(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", false
		}
		switch s[i] {
		case '"', '\\', '/':
			b.WriteByte(s[i])
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			r, ok := hex4(s, i+1)
			if !ok {
				return "", false
			}
			i += 4
			if utf16.IsSurrogate(r) {
				if r < 0xDC00 && i+6 < len(s) && s[i+1] == '\\' && s[i+2] == 'u' {
					if r2, ok := hex4(s, i+3); ok {
						if d := utf16.DecodeRune(r, r2); d != utf8.RuneError {
							b.WriteRune(d)
							i += 6
							continue
						}
					}
				}
				r = utf8.RuneError // lone surrogate
			}
			b.WriteRune(r)
		default:
			return "", false
		}
	}
	return b.String(), true
}

func hex4(s string, i int) (rune, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	var v rune
	for _, c := range []byte(s[i : i+4]) {
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

type member struct {
	key      string
	val      string // raw value text
	isString bool
}

// eachMember iterates the top-level members of a JSON object.
func eachMember(s string, fn func(m member) bool) error {
	i := skipWS(s, 0)
	if i >= len(s) || s[i] != '{' {
		return errNotJSON
	}
	i++
	for {
		i = skipWS(s, i)
		if i >= len(s) {
			return errors.New("unterminated JSON object")
		}
		if s[i] == '}' {
			return nil
		}
		if s[i] == ',' {
			i++
			continue
		}
		if s[i] != '"' {
			return errors.New("malformed JSON object")
		}
		ke, kesc, ok := scanString(s, i)
		if !ok {
			return errors.New("unterminated JSON key")
		}
		key := unquote(s[i:ke], kesc)
		i = skipWS(s, ke)
		if i >= len(s) || s[i] != ':' {
			return errors.New("malformed JSON member")
		}
		i = skipWS(s, i+1)
		ve, ok := skipValue(s, i)
		if !ok {
			return errors.New("malformed JSON value")
		}
		if !fn(member{key: key, val: s[i:ve], isString: s[i] == '"'}) {
			return nil
		}
		i = ve
	}
}

func jsonStr(m member) string {
	if !m.isString {
		return m.val
	}
	_, esc, _ := scanString(m.val, 0)
	return unquote(m.val, esc)
}

func timeFromJSON(m member) (time.Time, bool) {
	if m.isString {
		s := jsonStr(m)
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
		if t, n, ok := ParseTimestampPrefix(s); ok && n > 0 {
			return t, true
		}
		return time.Time{}, false
	}
	f, err := strconv.ParseFloat(m.val, 64)
	if err != nil || f <= 0 {
		return time.Time{}, false
	}
	switch {
	case f > 1e17:
		return time.Unix(0, int64(f)).UTC(), true
	case f > 1e14:
		return time.UnixMicro(int64(f)).UTC(), true
	case f > 1e11:
		return time.UnixMilli(int64(f)).UTC(), true
	default:
		return time.Unix(int64(f), int64((f-float64(int64(f)))*1e9)).UTC(), true
	}
}

func sevFromJSON(m member) engine.Severity {
	if m.isString {
		return engine.ParseSeverity(jsonStr(m))
	}
	n, err := strconv.Atoi(m.val)
	if err != nil {
		return engine.SevUnknown
	}
	switch { // bunyan/pino numeric levels
	case n <= 10:
		return engine.SevTrace
	case n <= 20:
		return engine.SevDebug
	case n <= 30:
		return engine.SevInfo
	case n <= 40:
		return engine.SevWarn
	case n <= 50:
		return engine.SevError
	default:
		return engine.SevFatal
	}
}

func (JSON) Parse(text string) (engine.LogRecord, error) {
	rec := engine.LogRecord{Message: text}
	var haveT, haveL, haveM, fromLog bool
	err := eachMember(text, func(m member) bool {
		switch strings.ToLower(m.key) {
		case "time", "timestamp", "ts", "@timestamp", "t", "datetime", "date":
			if !haveT {
				if t, ok := timeFromJSON(m); ok {
					rec.Timestamp, haveT = engine.TimestampOf(t), true
				}
			}
		case "level", "severity", "lvl", "loglevel", "levelname", "log.level":
			if !haveL {
				rec.Severity, haveL = sevFromJSON(m), true
			}
		case "message", "msg", "@message", "text", "event", "log":
			// "log" is Docker's json-file driver: {"log":"text\n","stream":"stdout","time":...}
			if !haveM && m.isString {
				rec.Message, haveM = jsonStr(m), true
				fromLog = strings.EqualFold(m.key, "log")
			}
		}
		return !(haveT && haveL && haveM)
	})
	if fromLog {
		// The application's own line: no trailing newline, and its level (Docker
		// records none) is read from the text itself.
		rec.Message = strings.TrimRight(rec.Message, "\r\n")
		if rec.Severity == engine.SevUnknown {
			if inner, _ := (Generic{}).Parse(rec.Message); inner.Severity != engine.SevUnknown || !inner.Timestamp.IsZero() {
				rec.Severity, rec.Message = inner.Severity, inner.Message
				if rec.Timestamp.IsZero() {
					rec.Timestamp = inner.Timestamp
				}
			}
		}
	}
	if err != nil {
		// Fall back to the generic view so the record is still readable.
		g, _ := Generic{}.Parse(text)
		g.ParseErr = true
		return g, err
	}
	return rec, nil
}

// Fields decodes the top-level members of a JSON record on demand. String
// values are unescaped; all other values are kept as engine.RawJSON.
func Fields(text string) (engine.Fields, error) {
	var out engine.Fields
	err := eachMember(text, func(m member) bool {
		if m.isString {
			out.Set(m.key, jsonStr(m), false)
		} else {
			out.Set(m.key, m.val, true)
		}
		return true
	})
	return out, err
}

// Field looks up one top-level field of a JSON record without building a map.
func Field(text, key string) (string, bool) {
	var res string
	var found bool
	if eachMember(text, func(m member) bool {
		if m.key == key {
			res, found = jsonStr(m), true
			return false
		}
		return true
	}) != nil && !found {
		return "", false
	}
	return res, found
}
