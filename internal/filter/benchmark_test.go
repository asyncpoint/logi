package filter

import (
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

func BenchmarkCompileAndMatch(b *testing.B) {
	rec := &engine.LogRecord{Severity: engine.SevError, Message: "connection reset by peer", Raw: engine.RawRecord{Data: `{"level":"ERROR","message":"connection reset by peer","service":"gateway"}`}, Fields: engine.Fields{{Key: "service", Value: "gateway"}}}
	for _, query := range []string{"severity:error connection", "service=gateway", `re:connection\s+reset`} {
		b.Run(query, func(b *testing.B) {
			p, err := Compile(query)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !p.Match(rec) {
					b.Fatal("expected match")
				}
			}
		})
	}
}
