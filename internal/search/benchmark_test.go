package search

import (
	"github.com/asyncpoint/logi/internal/engine"
	"testing"
)

func BenchmarkMatch(b *testing.B) {
	rec := &engine.LogRecord{Message: "connection reset by peer after request processing", Raw: engine.RawRecord{Data: "2026-10-09T10:11:12Z ERROR worker-7 connection reset by peer after request processing"}}
	for _, tc := range []struct {
		name string
		req  Request
	}{
		{"literal", Request{Query: "connection reset"}},
		{"regex", Request{Query: `connection\s+reset`, Mode: Regex}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			m, err := Compile(tc.req)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !m.Match(rec) {
					b.Fatal("expected match")
				}
			}
		})
	}
}
