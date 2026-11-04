package search

import (
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

// RawKeep must be a necessary condition of Match for every mode, and Spans must
// stay inside the string it was given.
func FuzzMatcherRawKeep(f *testing.F) {
	f.Add("timeout", "Timeout after 5s", uint8(0), false)
	f.Add("t.meout", "TIMEOUT", uint8(1), false)
	f.Add("(", "x", uint8(1), true)
	f.Add("a=b", `{"a":"b"}`, uint8(2), false)
	f.Fuzz(func(t *testing.T, q, raw string, mode uint8, cs bool) {
		m, err := Compile(Request{Query: q, Mode: Mode(mode % 3), CaseSensitive: cs})
		if err != nil {
			return
		}
		rec := &engine.LogRecord{Message: raw, Raw: engine.RawRecord{Data: raw}}
		if m.Match(rec) {
			if keep := m.RawKeep(); keep != nil && !keep(raw) {
				t.Fatalf("RawKeep rejected a matching record: %+v raw %q", m.Request(), raw)
			}
		}
		for _, sp := range m.Spans(raw) {
			if sp[0] < 0 || sp[1] > len(raw) || sp[0] > sp[1] {
				t.Fatalf("span %v outside %d-byte string", sp, len(raw))
			}
		}
	})
}
