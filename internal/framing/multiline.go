package framing

import "github.com/asyncpoint/logi/internal/engine"

// StartFunc reports whether a physical line begins a new logical record.
type StartFunc func(line []byte) bool

// MultilineFramer groups continuation lines (stack traces, wrapped messages)
// with the record that started them. A record stays open
// until the next start line, an idle Flush, or end of file.
type MultilineFramer struct {
	sc    lineScanner
	lim   engine.Limits
	start StartFunc
	skip  StartFunc // optional: lines dropped when no record is open (file headers/banners)
	term  StartFunc // optional: lines that close the open record and are dropped (e.g. "END")

	has      bool
	cur      []byte
	curOff   int64
	curLen   int64
	curLine  int64
	curLines int
	capped   bool // retention limit hit for the current record: later lines are not appended
	curTrunc bool
	f        Frame
}

func NewMultilineFramer(lim engine.Limits, offset int64, start StartFunc) *MultilineFramer {
	if lim.MaxRecordBytes <= 0 || lim.MaxLines <= 0 {
		lim = engine.DefaultLimits()
	}
	return &MultilineFramer{sc: newScanner(lim, offset, 1), lim: lim, start: start}
}

// SetSkip installs a predicate for lines that are not records at all, such as
// the column header some applications write at the top of every log file. A
// matching line is dropped only while no record is open, so a stack-trace line
// that happens to look like a header is never lost. Offsets and line numbers
// still advance across skipped lines.
func (m *MultilineFramer) SetSkip(f StartFunc) { m.skip = f }

// SetTerminator installs a predicate for marker lines that end a record, such as
// the "END" line NCache's event log writes after some entries. A matching line
// closes the open record (its bytes stay part of the record's span, but its text
// is not part of the record) and is not a record itself.
func (m *MultilineFramer) SetTerminator(f StartFunc) { m.term = f }

func (m *MultilineFramer) emitCur(emit func(*Frame)) {
	if !m.has {
		return
	}
	d := m.cur
	for len(d) > 0 && (d[len(d)-1] == '\n' || d[len(d)-1] == '\r') {
		d = d[:len(d)-1] // drop trailing blank continuation lines
	}
	m.f = Frame{Data: d, Offset: m.curOff, Length: m.curLen, Line: m.curLine, Lines: m.curLines, Truncated: m.curTrunc}
	m.has = false
	m.capped = false
	m.cur = m.cur[:0]
	emit(&m.f)
}

func (m *MultilineFramer) onLine(emit func(*Frame)) lineFn {
	return func(c []byte, off, raw, ln int64, tr bool) {
		if !m.has && m.skip != nil && len(c) > 0 && m.skip(c) {
			return
		}
		if m.term != nil && len(c) > 0 && m.term(c) {
			if m.has {
				m.curLen += raw
				m.curLines++
				m.emitCur(emit)
			}
			return
		}
		if len(c) > 0 && m.start(c) || (!m.has && len(c) > 0) {
			m.emitCur(emit)
			m.has = true
			m.capped = false
			m.cur = append(m.cur[:0], c...)
			m.curOff, m.curLen, m.curLine, m.curLines, m.curTrunc = off, raw, ln, 1, tr
			return
		}
		if !m.has {
			return // blank line before the first record
		}
		m.curLen += raw
		m.curLines++
		if tr {
			m.curTrunc = true
		}
		if m.capped || m.curLines > m.lim.MaxLines || len(m.cur)+1+len(c) > m.lim.MaxRecordBytes {
			// Once any line is dropped, drop every later one too: a record with a
			// silent hole in the middle is worse than a clean prefix.
			m.capped = true
			m.curTrunc = true
			return // keep counting the span, stop retaining text
		}
		m.cur = append(m.cur, '\n')
		m.cur = append(m.cur, c...)
	}
}

func (m *MultilineFramer) Push(data []byte, emit func(*Frame)) { m.sc.scan(data, m.onLine(emit)) }

func (m *MultilineFramer) Flush(final bool, emit func(*Frame)) {
	if final {
		m.sc.flushPartial(m.onLine(emit))
	}
	m.emitCur(emit)
}

func (m *MultilineFramer) Pending() bool { return m.has }

func (m *MultilineFramer) Reset(offset, line int64) {
	m.sc.reset(offset, line)
	m.has = false
	m.cur = m.cur[:0]
}
