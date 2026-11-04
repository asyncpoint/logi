package ncache

import (
	"strings"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/parser"
)

// Parser parses NCache records for one table layout. It is immutable and safe
// for concurrent use.
type Parser struct {
	name string
	lay  layout
}

func (p Parser) Name() string {
	if p.name == "" {
		return Name
	}
	return p.name
}

func (p Parser) layout() layout {
	if len(p.lay.names) == 0 {
		return cacheLogLayout
	}
	return p.lay
}

func (p Parser) Parse(text string) (engine.LogRecord, error) {
	rec := engine.LogRecord{Message: text}
	first := text
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		first, rec.Multiline = strings.TrimRight(text[:nl], "\r"), true
	}
	ts, n, ok := parser.ParseTimestampPrefix(first)
	if !ok {
		rec.ParseErr = true
		return rec, errNoTimestamp
	}
	rec.Timestamp = engine.TimestampOf(ts)
	tail := text[len(first):] // continuation lines, kept with the message
	lay := p.layout()
	if sev, fields, msg, ok := parseColumns(first, lay); ok {
		rec.Severity, rec.Fields, rec.Message = sev, fields, msg+tail
		addException(&rec, tail)
		return rec, nil
	}
	if sev, fields, msg, ok := parseRuns(first, n, lay); ok { // widths differ from the layout: split on gaps
		rec.Severity, rec.Fields, rec.Message = sev, fields, msg+tail
		addException(&rec, tail)
		return rec, nil
	}
	sev, fields, msg := parseFlat(first, n) // older "ts LEVEL [thread] message" layout
	rec.Severity, rec.Fields, rec.Message = sev, fields, msg+tail
	addException(&rec, tail)
	return rec, nil
}

func addException(rec *engine.LogRecord, tail string) {
	if rec.Multiline {
		if ex := exceptionName(tail); ex != "" {
			rec.Fields.Set("exception", ex, false)
		}
	}
}

func trim(s string) string { return strings.Trim(s, " \t") }

func slice(s string, from, to int) string {
	if from >= len(s) {
		return ""
	}
	if to > len(s) || to < 0 {
		to = len(s)
	}
	return s[from:to]
}

// gap returns the byte range of the first run of two or more spaces (or a tab)
// in s, which separates columns whose padding does not match the layout.
func gap(s string) (start, end int, ok bool) {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\t':
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			return i, j, true
		case s[i] == ' ' && i+1 < len(s) && s[i+1] == ' ':
			j := i + 2
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			return i, j, true
		}
	}
	return 0, 0, false
}

// splitEvent splits the cache log's MESSAGE column into "event name" and text.
func splitEvent(region string) (event, msg string) {
	region = strings.TrimLeft(region, " \t")
	if s, e, ok := gap(region); ok {
		return trim(region[:s]), region[e:]
	}
	return "", region
}

// finishMessage turns the MESSAGE column into (event, text) or plain text.
func finishMessage(lay layout, fields *engine.Fields, region string) string {
	if lay.splitEvent {
		event, msg := splitEvent(region)
		if event != "" {
			fields.Set("event", event, false)
		}
		return msg
	}
	return strings.TrimLeft(region, " \t")
}

// parseColumns slices a record by the layout's offsets and validates it: every
// column boundary must be a space and the LEVEL column must hold a level.
func parseColumns(line string, lay layout) (engine.Severity, engine.Fields, string, bool) {
	o, li := lay.off, lay.levelIdx()
	last := len(o) - 1
	if li < 1 || li >= last || len(line) <= o[li] {
		return 0, nil, "", false
	}
	for i := 1; i <= last; i++ {
		if o[i]-1 < len(line) && line[o[i]-1] != ' ' && line[o[i]-1] != '\t' {
			return 0, nil, "", false
		}
	}
	sev := engine.ParseSeverity(trim(slice(line, o[li], o[li+1])))
	if sev == engine.SevUnknown {
		return 0, nil, "", false
	}
	fields := make(engine.Fields, 0, 4)
	for i := 1; i < li; i++ {
		if v := trim(slice(line, o[i], o[i+1])); v != "" {
			fields.Set(lay.names[i], v, false)
		}
	}
	return sev, fields, finishMessage(lay, &fields, slice(line, o[last], -1)), true
}

type span struct{ s, e int }

// parseRuns handles rows whose widths do not match the layout: it splits after
// the timestamp on runs of 2+ spaces and finds the LEVEL field by its value. The
// fields before it are named right-to-left from the layout.
func parseRuns(line string, afterTS int, lay layout) (engine.Severity, engine.Fields, string, bool) {
	var fields []span
	i := afterTS
	for i < len(line) && len(fields) < 8 {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		if s, e, ok := gap(line[i:]); ok {
			fields = append(fields, span{start, i + s})
			i += e
		} else {
			fields = append(fields, span{start, len(line)})
			i = len(line)
		}
	}
	li := lay.levelIdx()
	for idx, f := range fields {
		if idx == 0 || idx > 4 {
			continue
		}
		sev := engine.ParseSeverity(line[f.s:f.e])
		if sev == engine.SevUnknown {
			continue
		}
		out := make(engine.Fields, 0, 4)
		for k := 1; k < li; k++ { // names between TIMESTAMP and LEVEL, aligned to the fields before the level
			if fi := idx - (li - k); fi >= 0 {
				if v := line[fields[fi].s:fields[fi].e]; v != "" {
					out.Set(lay.names[k], v, false)
				}
			}
		}
		region := ""
		if idx+1 < len(fields) {
			region = line[fields[idx+1].s:]
		}
		return sev, out, finishMessage(lay, &out, region), true
	}
	return 0, nil, "", false
}

// parseFlat understands "timestamp LEVEL [thread] message", as written by
// simpler NCache/log4net configurations.
func parseFlat(line string, afterTS int) (engine.Severity, engine.Fields, string) {
	pos := skipSpaces(line, afterTS)
	end := pos
	for end < len(line) && line[end] != ' ' && line[end] != '\t' {
		end++
	}
	var sev engine.Severity
	if s := engine.ParseSeverity(strings.Trim(line[pos:end], "[]:")); s != engine.SevUnknown {
		sev, pos = s, skipSpaces(line, end)
	}
	fields := make(engine.Fields, 0, 1)
	if pos < len(line) && line[pos] == '[' {
		if c := strings.IndexByte(line[pos:], ']'); c > 0 {
			fields.Set("thread", line[pos+1:pos+c], false)
			pos = skipSpaces(line, pos+c+1)
		}
	}
	return sev, fields, line[pos:]
}

// exceptionName returns the first dotted identifier ending in "Exception"
// (e.g. "Alachisoft.NCache.Runtime.Exceptions.OperationFailedException") found
// at the start of the first few continuation lines.
func exceptionName(rest string) string {
	for _, line := range strings.SplitN(strings.TrimLeft(rest, "\r\n"), "\n", 4) {
		line = strings.TrimSpace(line)
		end := 0
		for end < len(line) && (isIdent(line[end]) || line[end] == '.') {
			end++
		}
		if tok := line[:end]; strings.HasSuffix(tok, "Exception") {
			return tok
		}
	}
	return ""
}

func isIdent(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '-') {
		i++
	}
	return i
}

type parseError string

func (e parseError) Error() string { return string(e) }

const errNoTimestamp = parseError("ncache: record does not start with a timestamp")

var _ parser.Parser = Parser{}
