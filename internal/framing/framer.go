// Package framing turns a byte stream into logical records.
// It never parses: it only decides where one record ends and the next begins.
package framing

import (
	"bytes"

	"github.com/asyncpoint/logi/internal/engine"
)

// Frame is one logical record. Data is only valid during the emit callback;
// consumers must copy it (typically with string(Data), once).
type Frame struct {
	Data      []byte // record text without the trailing line terminator
	Offset    int64  // absolute byte offset of the first byte of the record
	Length    int64  // physical span in bytes, including terminators
	Line      int64  // 1-based number of the first physical line
	Lines     int    // physical lines in the record
	Truncated bool   // Data was cut by a limit
}

// Framer incrementally frames a byte stream.
type Framer interface {
	// Push feeds bytes, which may end in the middle of a line.
	Push(data []byte, emit func(*Frame))
	// Flush emits a record that is complete but still held open awaiting a
	// possible continuation (idle flush). With final=true (end of a finished
	// file) an unterminated trailing line is emitted as well.
	Flush(final bool, emit func(*Frame))
	// Pending reports whether a complete record is being held open.
	Pending() bool
	// Reset discards all buffered state and restarts at the given offset/line.
	Reset(offset, line int64)
}

// lineScanner splits a stream into physical lines while buffering a partial
// trailing line. The partial buffer is bounded by MaxRecordBytes;
// beyond that, bytes are discarded but still counted so offsets stay exact.
type lineScanner struct {
	maxLine int
	partial []byte
	pstart  int64 // offset of first byte of partial
	plen    int64 // physical length of partial (including discarded bytes)
	pover   bool
	pos     int64 // offset of the next byte to be pushed
	line    int64 // 1-based number of the next line to complete
}

func newScanner(lim engine.Limits, offset, line int64) lineScanner {
	if lim.MaxRecordBytes <= 0 {
		lim = engine.DefaultLimits()
	}
	if line < 1 {
		line = 1
	}
	return lineScanner{maxLine: lim.MaxRecordBytes, pos: offset, line: line}
}

func (s *lineScanner) reset(offset, line int64) {
	s.partial = s.partial[:0]
	s.plen, s.pover, s.pos = 0, false, offset
	if line < 1 {
		line = 1
	}
	s.line = line
}

func (s *lineScanner) addPartial(seg []byte) {
	s.plen += int64(len(seg))
	if s.pover {
		return
	}
	room := s.maxLine - len(s.partial)
	if len(seg) <= room {
		s.partial = append(s.partial, seg...)
		return
	}
	s.partial = append(s.partial, seg[:room]...)
	s.pover = true
}

func trimCR(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\r' {
		return b[:n-1]
	}
	return b
}

type lineFn func(content []byte, off, rawLen, lineNo int64, trunc bool)

func (s *lineScanner) scan(data []byte, fn lineFn) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if s.plen == 0 {
				s.pstart = s.pos
			}
			s.addPartial(data)
			s.pos += int64(len(data))
			return
		}
		var (
			content []byte
			off     int64
			rawLen  int64
			trunc   bool
		)
		if s.plen > 0 {
			s.addPartial(data[:i])
			content, off, rawLen, trunc = s.partial, s.pstart, s.plen+1, s.pover
		} else {
			content, off, rawLen = data[:i], s.pos, int64(i)+1
			if len(content) > s.maxLine {
				content, trunc = content[:s.maxLine], true
			}
		}
		fn(trimCR(content), off, rawLen, s.line, trunc)
		s.line++
		s.partial = s.partial[:0]
		s.plen, s.pover = 0, false
		s.pos += int64(i) + 1
		data = data[i+1:]
	}
}

// flushPartial emits an unterminated trailing line (end of a finished file).
func (s *lineScanner) flushPartial(fn lineFn) {
	if s.plen == 0 {
		return
	}
	fn(trimCR(s.partial), s.pstart, s.plen, s.line, s.pover)
	s.line++
	s.partial = s.partial[:0]
	s.plen, s.pover = 0, false
}
