package input

import (
	"bytes"
	"regexp"
	"strings"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
	"github.com/asyncpoint/logi/internal/parser"
)

// Container tooling decorates every line it prints:
//
//	docker compose logs      web-1  | the application's line
//	kubectl logs --prefix    [pod/web-7d9f/web] the application's line
//	docker logs -t           2026-10-15T10:00:00.123456789Z the application's line
//
// The decoration hides the application's own timestamp and level from the parsers
// and mixes several services into one stream. For piped input, logi recognises
// these layouts, removes the decoration, uses the service or pod as the record's
// source (so each one gets its own colour, like in the multi-file view) and frames
// multi-line records per source, because the stack traces of different services
// interleave on the wire.

type srcKind int

const (
	srcNone    srcKind = iota
	srcCompose         // web-1  | message
	srcKube            // [pod/web-7d9f/web] message
)

// decorStyle is the detected decoration of a stream.
type decorStyle struct {
	src   srcKind
	stamp bool // a Docker/kubectl timestamp in front of lines that carry their own
}

func (s decorStyle) active() bool { return s.src != srcNone || s.stamp }

var (
	composeRE = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*) +\| ?(.*)$`)
	kubeRE    = regexp.MustCompile(`^\[((?:pod/)?[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)+)\] ?(.*)$`)
	stampRE   = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z) (.*)$`)
	strictRE  = regexp.MustCompile(`[-_]\d+$`) // compose names its containers service-1 / service_1
)

// stripSource removes the source prefix, returning the source name.
func (s decorStyle) stripSource(line string) (src, rest string) {
	switch s.src {
	case srcCompose:
		if m := composeRE.FindStringSubmatch(line); m != nil {
			return m[1], m[2]
		}
	case srcKube:
		if m := kubeRE.FindStringSubmatch(line); m != nil {
			return strings.TrimPrefix(m[1], "pod/"), m[2]
		}
	}
	return "", line
}

// strip removes the decoration from one line. hint is the time the tooling
// stamped on the line, to be used when the application's own text has none.
func (s decorStyle) strip(line string) (src, rest string, hint engine.Timestamp) {
	src, rest = s.stripSource(line)
	if s.stamp {
		if m := stampRE.FindStringSubmatch(rest); m != nil {
			if t, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
				hint = engine.TimestampOf(t)
			}
			rest = m[2]
		}
	}
	return src, rest, hint
}

// sampleLines returns up to max complete, non-empty lines of data without
// terminal escapes (a trailing unterminated line is ignored unless it is all
// there is).
func sampleLines(data []byte, max int) []string {
	parts := strings.Split(string(data), "\n")
	if len(parts) > 1 && !bytes.HasSuffix(data, []byte("\n")) {
		parts = parts[:len(parts)-1]
	}
	var out []string
	for _, p := range parts {
		p = engine.StripANSI(strings.TrimRight(p, "\r"))
		if strings.TrimSpace(p) == "" {
			continue
		}
		if out = append(out, p); len(out) == max {
			break
		}
	}
	return out
}

// detectStyle decides, from the start of a stream, whether it is decorated. It
// must be conservative: a false positive would mangle an ordinary log, so a style
// needs nearly every sampled line to match, and several look-alikes are excluded
// (a bracketed timestamp is not a pod prefix; a table is not compose output; a
// log that merely starts its lines with a timestamp has no second layer).
func detectStyle(data []byte) decorStyle {
	lines := sampleLines(data, 40)
	n := len(lines)
	if n == 0 {
		return decorStyle{}
	}
	need := (n*9 + 9) / 10 // 90%, rounded up
	var composeN, kubeN, strictN int
	names := map[string]bool{}
	for _, l := range lines {
		if m := composeRE.FindStringSubmatch(l); m != nil {
			composeN++
			names[m[1]] = true
			if strictRE.MatchString(m[1]) {
				strictN++
			}
		}
		if kubeRE.MatchString(l) {
			kubeN++
		}
	}
	var st decorStyle
	switch {
	case composeN >= need && (strictN == composeN || (n >= 6 && len(names)*2 <= n)):
		// all names look like compose containers (web-1), or a few names repeat
		// over many lines (labels, not the row values of a table)
		st.src = srcCompose
	case kubeN >= need:
		st.src = srcKube
	}
	var stamped, nested int
	for _, l := range lines {
		_, rest := st.stripSource(l)
		if m := stampRE.FindStringSubmatch(rest); m != nil {
			stamped++
			if _, _, ok := parser.ParseTimestampPrefix(m[2]); ok {
				nested++ // the application stamps its own lines too: a second layer
			}
		}
	}
	st.stamp = stamped >= need && nested >= 1
	return st
}

// stripSample removes the decoration from the complete lines of the detection
// sample, so the format and framing are detected from the application's text.
func (s decorStyle) stripSample(data []byte) []byte {
	var out bytes.Buffer
	rest := string(data)
	for len(rest) > 0 {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			out.WriteString(rest) // unterminated tail: leave it as it is
			break
		}
		line := engine.StripANSI(strings.TrimRight(rest[:i], "\r"))
		rest = rest[i+1:]
		_, text, _ := s.strip(line)
		out.WriteString(text)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

const (
	maxDecorSources = 256     // distinct sources tracked; the rest share "other"
	maxDecorLine    = 1 << 20 // an unterminated line this long is flushed as a line
)

// srcFramer is the framing state of one source.
type srcFramer struct {
	name  string
	fr    framing.Framer
	hints []engine.Timestamp // hints[i] belongs to line base+i
	base  int64
}

// decorator splits a decorated stream into lines, strips each, and feeds one
// framer per source.
type decorator struct {
	style   decorStyle
	t       *Tailer
	carry   []byte
	framers map[string]*srcFramer
	order   []*srcFramer // creation order, so flushing is deterministic
}

func newDecorator(style decorStyle, t *Tailer) *decorator {
	return &decorator{style: style, t: t, framers: map[string]*srcFramer{}}
}

func (d *decorator) framerFor(src string) *srcFramer {
	if sf, ok := d.framers[src]; ok {
		return sf
	}
	if len(d.framers) >= maxDecorSources {
		src = "other"
		if sf, ok := d.framers[src]; ok {
			return sf
		}
	}
	var fr framing.Framer
	if d.t.o.LineFraming {
		fr = framing.NewLineFramer(d.t.o.Limits, 0)
	} else {
		fr = d.t.profile.Framer(d.t.sample, d.t.o.Limits, 0)
	}
	sf := &srcFramer{name: src, fr: fr, base: 1}
	d.framers[src] = sf
	d.order = append(d.order, sf)
	if d.style.src != srcNone {
		d.t.srcCount.Store(int64(len(d.framers)))
	}
	return sf
}

func (d *decorator) push(data []byte) {
	d.carry = append(d.carry, data...)
	start := 0
	for {
		i := bytes.IndexByte(d.carry[start:], '\n')
		if i < 0 {
			break
		}
		d.line(string(bytes.TrimRight(d.carry[start:start+i], "\r")))
		start += i + 1
	}
	d.carry = append(d.carry[:0], d.carry[start:]...)
	if len(d.carry) > maxDecorLine {
		d.line(string(d.carry))
		d.carry = d.carry[:0]
	}
}

func (d *decorator) line(l string) {
	src, rest, hint := d.style.strip(engine.StripANSI(l))
	if src == "" {
		src = d.t.o.SourceID
	}
	sf := d.framerFor(src)
	sf.hints = append(sf.hints, hint)
	sf.fr.Push([]byte(rest+"\n"), func(f *framing.Frame) { d.emit(sf, f) })
}

// emit hands a finished record on, with the time hint of its first line, and
// forgets the hints of the lines it covered.
func (d *decorator) emit(sf *srcFramer, f *framing.Frame) {
	var hint engine.Timestamp
	if i := f.Line - sf.base; i >= 0 && i < int64(len(sf.hints)) {
		hint = sf.hints[i]
	}
	if drop := f.Line + int64(f.Lines) - sf.base; drop > 0 && drop <= int64(len(sf.hints)) {
		n := copy(sf.hints, sf.hints[drop:])
		sf.hints, sf.base = sf.hints[:n], sf.base+drop
	}
	src := sf.name
	if src == d.t.o.SourceID && d.style.src == srcNone {
		src = "" // only a stamp layer: the source stays the stream's own
	}
	d.t.collectWith(f, src, hint)
}

func (d *decorator) flushAll(force bool) {
	if force && len(d.carry) > 0 { // the input ended in the middle of a line: it is still a line
		d.line(string(bytes.TrimRight(d.carry, "\r")))
		d.carry = d.carry[:0]
	}
	for _, sf := range d.order {
		sf := sf
		sf.fr.Flush(force, func(f *framing.Frame) { d.emit(sf, f) })
	}
}

func (d *decorator) pending() bool {
	for _, sf := range d.order {
		if sf.fr.Pending() {
			return true
		}
	}
	return false
}
