package input

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/parser"
)

// A quiet source must not allocate a full-size batch per flush.
func TestTailerQuietSourceAllocatesLittle(t *testing.T) {
	if testing.Short() {
		t.Skip("timing based")
	}
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("2026-10-15 10:00:00 INFO start\n"), 0o644)
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	ring := engine.NewRingStore(10000, 0, &engine.Stats{})
	tl, err := NewTailer(TailOptions{Path: p, SourceID: "a", Registry: reg, Store: ring, Sink: nopSink2{}, Backlog: -1,
		ForcePoll: true, PollInterval: 20 * time.Millisecond, Coalesce: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()
	time.Sleep(200 * time.Millisecond)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() // an open handle blocks TempDir cleanup on Windows
	const lines = 50
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	for i := 0; i < lines; i++ {
		fmt.Fprintf(f, "2026-10-15 10:00:%02d INFO line %d\n", i%60, i)
		time.Sleep(40 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	runtime.ReadMemStats(&m1)
	cancel()
	<-done
	if per := (m1.TotalAlloc - m0.TotalAlloc) / lines; per > 16<<10 {
		t.Fatalf("%d bytes allocated per record on a quiet source (was ~115000 before the fix)", per)
	}
}

type nopSink2 struct{}

func (nopSink2) Added(uint64, uint64, int) {}
func (nopSink2) State(SourceState, string) {}
