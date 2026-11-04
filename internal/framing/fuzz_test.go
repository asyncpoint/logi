package framing

import (
	"fmt"
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

// Framing must not depend on how the input was split into reads, must account
// stay inside the input without overlapping, and must respect the record limits.
func FuzzFramerSplitInvariance(f *testing.F) {
	f.Add("2026-10-15 10:00:00 INFO a\n  at x\n2026-10-15 10:00:01 INFO b\n", uint8(3))
	f.Add("no timestamps\r\nat all\n\n", uint8(1))
	f.Add("2026-10-15 10:00:00 partial line without newline", uint8(7))
	lim := engine.Limits{MaxRecordBytes: 64, MaxLines: 4}
	f.Fuzz(func(t *testing.T, in string, step uint8) {
		type fr struct {
			data       string
			off, ln, n int64
		}
		run := func(fz Framer, chunk int) []fr {
			var out []fr
			emit := func(x *Frame) { out = append(out, fr{string(x.Data), x.Offset, x.Line, int64(x.Length)}) }
			b := []byte(in)
			for len(b) > 0 {
				n := min(chunk, len(b))
				fz.Push(b[:n], emit)
				b = b[n:]
			}
			fz.Flush(true, emit)
			return out
		}
		for name, mk := range map[string]func() Framer{
			"multiline": func() Framer { return NewMultilineFramer(lim, 0, LooksLikeTimestamp) },
			"line":      func() Framer { return NewLineFramer(lim, 0) },
		} {
			whole := run(mk(), len(in)+1)
			part := run(mk(), int(step)%13+1)
			if fmt.Sprint(whole) != fmt.Sprint(part) {
				t.Fatalf("%s: framing depends on read size for %q", name, in)
			}
			// Blank lines are dropped on purpose, so gaps are legal; overlaps,
			// reordering and reading past the input are not.
			var next int64
			for _, r := range whole {
				if r.off < next || r.n <= 0 {
					t.Fatalf("%s: overlap/reorder at offset %d (min %d) for %q", name, r.off, next, in)
				}
				next = r.off + r.n
				if len(r.data) > lim.MaxRecordBytes {
					t.Fatalf("%s: record of %d bytes exceeds limit", name, len(r.data))
				}
			}
			if next > int64(len(in)) {
				t.Fatalf("%s: frames extend to %d beyond the %d-byte input %q", name, next, len(in), in)
			}
		}
	})
}
