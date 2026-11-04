package engine

import (
	"fmt"
	"testing"
)

func BenchmarkRingAppendCapacity(b *testing.B) {
	for _, capacity := range []int{1024, 65536} {
		b.Run(fmt.Sprintf("capacity-%d", capacity), func(b *testing.B) {
			ring := NewRingStore(capacity, 0, nil)
			rec := LogRecord{Severity: SevInfo, Message: "request completed status=200", Raw: RawRecord{Data: "2026-10-01T12:34:56Z INFO request completed status=200"}, SourceID: "service.log"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ring.Append(rec)
			}
		})
	}
}

func BenchmarkRingGetAndRange(b *testing.B) {
	const count = 10000
	ring := NewRingStore(count, 0, nil)
	for i := 0; i < count; i++ {
		ring.Append(LogRecord{Severity: SevInfo, Message: "request completed", Raw: RawRecord{Data: "request completed"}})
	}
	b.Run("Get", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := ring.Get(uint64(i%count + 1)); !ok {
				b.Fatal("record missing")
			}
		}
	})
	b.Run("Range100", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ring.Range(1, 100, func(*LogRecord) bool { return true })
		}
	})
}
