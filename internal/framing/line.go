package framing

import "github.com/asyncpoint/logi/internal/engine"

// LineFramer emits one record per non-empty physical line.
// Empty lines are skipped (their bytes still advance offsets).
type LineFramer struct {
	sc lineScanner
	f  Frame
}

func NewLineFramer(lim engine.Limits, offset int64) *LineFramer {
	return &LineFramer{sc: newScanner(lim, offset, 1)}
}

func (l *LineFramer) onLine(emit func(*Frame)) lineFn {
	return func(c []byte, off, raw, ln int64, tr bool) {
		if len(c) == 0 {
			return
		}
		l.f = Frame{Data: c, Offset: off, Length: raw, Line: ln, Lines: 1, Truncated: tr}
		emit(&l.f)
	}
}

func (l *LineFramer) Push(data []byte, emit func(*Frame)) { l.sc.scan(data, l.onLine(emit)) }

func (l *LineFramer) Flush(final bool, emit func(*Frame)) {
	if final {
		l.sc.flushPartial(l.onLine(emit))
	}
}

func (l *LineFramer) Pending() bool { return false }

func (l *LineFramer) Reset(offset, line int64) { l.sc.reset(offset, line) }
