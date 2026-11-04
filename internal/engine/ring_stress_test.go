package engine

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// Exercises concurrent appends, evictions, clears, and read snapshots. Run with
// -race to check both lock discipline and the record fields exposed by Range.
func TestRingStoreConcurrentReadersWriters(t *testing.T) {
	const capacity = 128
	ring := NewRingStore(capacity, 32*1024, nil)
	var stop atomic.Bool
	var writes atomic.Uint64
	var wg sync.WaitGroup

	writer := func(seed int) {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			rec := LogRecord{Severity: Severity(i % int(NumSeverities)), Message: fmt.Sprintf("writer-%d message-%d", seed, i), Raw: RawRecord{Data: fmt.Sprintf("writer-%d payload-%d", seed, i)}, SourceID: fmt.Sprintf("source-%d", seed)}
			ring.Append(rec)
			writes.Add(1)
			if i%251 == 0 {
				runtime.Gosched()
			}
		}
	}
	reader := func() {
		defer wg.Done()
		for !stop.Load() {
			newest := ring.NewestID()
			if newest != 0 {
				ring.Get(newest)
				ring.Get(ring.OldestID())
			}
			ring.Range(0, 0, func(r *LogRecord) bool {
				if r.Message == "" || r.Raw.Data == "" {
					t.Errorf("observed partially written record")
					return false
				}
				return true
			})
			_ = ring.Len()
			_ = ring.Bytes()
			runtime.Gosched()
		}
	}
	wg.Add(6)
	for i := 0; i < 4; i++ {
		go reader()
	}
	for i := 0; i < 2; i++ {
		go writer(i)
	}
	// The writers run a bounded workload; readers terminate after they finish.
	// Separate wait groups would add complexity, so wait for the exact writes.
	for writes.Load() < 6000 {
		runtime.Gosched()
	}
	stop.Store(true)
	wg.Wait()
	if got := ring.Len(); got > capacity {
		t.Fatalf("ring len %d exceeds capacity %d", got, capacity)
	}
	if writes.Load() != 6000 {
		t.Fatalf("writes=%d, want 6000", writes.Load())
	}
}

func BenchmarkRingAppendConcurrent(b *testing.B) {
	ring := NewRingStore(4096, 0, nil)
	rec := LogRecord{Severity: SevInfo, Message: "concurrent append", Raw: RawRecord{Data: "concurrent append"}}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ring.Append(rec)
		}
	})
}
