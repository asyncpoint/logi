package input

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/parser"
)

// A file rewritten in place (same inode, regrown past the old offset between
// polls) must be re-read from the top instead of silently losing lines.
func TestTailerDetectsInPlaceRewrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	var old strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&old, "2026-10-15 10:00:0%d INFO OLD line %d\n", i, i)
	}
	os.WriteFile(p, []byte(old.String()), 0o644)
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	ring := engine.NewRingStore(1000, 0, &engine.Stats{})
	tl, err := NewTailer(TailOptions{Path: p, SourceID: "a", Registry: reg, Store: ring, Sink: nopSink2{}, Backlog: 1 << 20,
		ForcePoll: true, PollInterval: 20 * time.Millisecond, Coalesce: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "initial lines", func() bool { return ring.Len() == 5 })

	// Overwrite from offset 0 without shrinking: size only grows.
	var nw strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&nw, "2026-10-15 11:00:%02d INFO NEW line %d\n", i, i)
	}
	f, _ := os.OpenFile(p, os.O_WRONLY, 0o644)
	f.WriteAt([]byte(nw.String()), 0)
	f.Close()

	waitFor(t, "rewritten lines re-read", func() bool {
		n := 0
		ring.Range(0, 0, func(r *engine.LogRecord) bool {
			if strings.Contains(r.Message, "NEW line") {
				n++
			}
			return true
		})
		return n == 12
	})
}
