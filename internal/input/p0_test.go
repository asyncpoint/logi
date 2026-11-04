package input

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/plugins"
)

type quietSink struct{}

func (quietSink) Added(uint64, uint64, int) {}
func (quietSink) State(SourceState, string) {}

// Small pipe reads must not each cost a ChunkSize allocation.
func TestStdinSmallReadsDoNotAmplifyAllocation(t *testing.T) {
	reg, _ := plugins.BuildRegistry(nil)
	ring := engine.NewRingStore(10000, 8<<20, &engine.Stats{})
	pr, pw := io.Pipe()
	tl, err := NewTailer(TailOptions{SourceID: "stdin", Registry: reg, Store: ring, Sink: quietSink{}, Reader: pr})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()

	chunk := []byte(strings.Repeat("2026-09-19 08:00:00,000 INFO [w-7] request handled in 12ms path=/api/v1/items\n", 50))
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	const writes = 2000
	for i := 0; i < writes; i++ {
		pw.Write(chunk)
	}
	pw.Close()
	deadline := time.Now().Add(5 * time.Second)
	for ring.Len() < 10000 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	runtime.ReadMemStats(&m1)
	cancel()
	<-done
	in := float64(writes * len(chunk))
	if ratio := float64(m1.TotalAlloc-m0.TotalAlloc) / in; ratio > 8 {
		t.Fatalf("allocated %.1fx the input size; want <= 8x", ratio)
	}
	if ring.Len() != 10000 {
		t.Fatalf("records = %d", ring.Len())
	}
}

// A cancelled context must stop a long backlog read at the next chunk.
func TestReadAvailableStopsWhenCancelled(t *testing.T) {
	reg, _ := plugins.BuildRegistry(nil)
	p := filepath.Join(t.TempDir(), "big.log")
	if err := os.WriteFile(p, []byte(strings.Repeat("2026-09-19 08:00:00 INFO hello\n", 100000)), 0o644); err != nil {
		t.Fatal(err)
	}
	ring := engine.NewRingStore(1000, 1<<20, &engine.Stats{})
	tl, err := NewTailer(TailOptions{Path: p, SourceID: "big", Registry: reg, Store: ring, Sink: quietSink{}, ChunkSize: 4096})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tl.ctx = ctx
	f, _ := os.Open(p)
	defer f.Close()
	tl.f = f
	tl.readAvailable()
	if tl.offset != 0 {
		t.Fatalf("read %d bytes after cancellation", tl.offset)
	}
	close(tl.batches)
}
