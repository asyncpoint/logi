package input

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/parser"
)

func TestBacklogForLines(t *testing.T) {
	for _, c := range []struct {
		data string
		n    int
		want int64
	}{
		{"l1\nl2\nl3\n", 2, 6},  // "l2\nl3\n"
		{"l1\nl2\nl3\n", 1, 3},  // "l3\n"
		{"l1\nl2\nl3\n", 3, 9},  // the whole file
		{"l1\nl2\nl3\n", 10, 9}, // fewer lines than asked: everything
		{"l1\nl2\nl3", 1, 2},    // no trailing newline: "l3"
		{"l1\nl2\nl3", 2, 5},    // "l2\nl3"
		{"l1\r\nl2\r\n", 1, 4},  // CRLF: "l2\r\n"
		{"l1\n\n\nl4\n", 2, 4},  // blank lines are lines: "\nl4\n"... counts 2 lines from the end
		{"", 5, 0},
		{"abc\n", 0, 0},          // 0 lines: only new data
		{"just one line", 3, 13}, // no newline at all
	} {
		got := backlogForLines(bytes.NewReader([]byte(c.data)), int64(len(c.data)), c.n)
		if got != c.want {
			t.Errorf("backlogForLines(%q, %d) = %d, want %d", c.data, c.n, got, c.want)
		}
	}
	// across read-block boundaries
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "line %06d\n", i)
	}
	data := []byte(b.String())
	for _, n := range []int{1, 7, 5000, 19999, 20000, 25000} {
		got := backlogForLines(bytes.NewReader(data), int64(len(data)), n)
		tail := string(data[int64(len(data))-got:])
		wantLines := n
		if wantLines > 20000 {
			wantLines = 20000
		}
		if strings.Count(tail, "\n") != wantLines || !strings.HasSuffix(tail, "line 019999\n") {
			t.Errorf("n=%d: tail has %d lines, want %d", n, strings.Count(tail, "\n"), wantLines)
		}
	}
}

func TestLastWallNano(t *testing.T) {
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	wall := func(s string) int64 {
		tm, _ := time.Parse("2006-01-02 15:04:05", s)
		return tm.UnixNano()
	}
	var text strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&text, "2026-10-15 10:%02d:00 INFO line %d\n", i, i)
	}
	// the newest record ends with a stack trace: its continuation lines have no time
	text.WriteString("2026-10-15 10:59:30 ERROR boom\n\tat A.b(A.java:1)\n\tat C.d(C.java:2)\n")
	if got, ok := LastWallNano(write("a.log", text.String()), reg, ""); !ok || got != wall("2026-10-15 10:59:30") {
		t.Fatalf("text log: got %d ok=%v, want the ERROR record's time", got, ok)
	}
	js := `{"time":"2026-10-15T08:00:00Z","level":"info","msg":"a"}` + "\n" + `{"time":"2026-10-15T08:30:00Z","level":"info","msg":"b"}` + "\n"
	if got, ok := LastWallNano(write("b.jsonl", js), reg, ""); !ok || got != wall("2026-10-15 08:30:00") {
		t.Fatalf("json log: got %d ok=%v", got, ok)
	}
	if _, ok := LastWallNano(write("c.log", "no timestamps here\nnone here either\n"), reg, ""); ok {
		t.Fatal("a log without timestamps has no anchor")
	}
	if _, ok := LastWallNano(write("empty.log", ""), reg, ""); ok {
		t.Fatal("an empty file has no anchor")
	}
	if _, ok := LastWallNano(filepath.Join(dir, "missing.log"), reg, ""); ok {
		t.Fatal("a missing file has no anchor")
	}
	// a large file: only the tail is read, and a record straddling the read start is not mistaken
	var big strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&big, "2026-10-15 %02d:%02d:%02d INFO padding line %d\n", i/3600%24, i/60%60, i%60, i)
	}
	if got, ok := LastWallNano(write("big.log", big.String()), reg, ""); !ok || got != wall("2026-10-15 05:33:19") {
		t.Fatalf("big log: got %d ok=%v", got, ok)
	}
}

type recSink struct {
	mu     sync.Mutex
	states []string
	added  int
}

func (s *recSink) Added(first, last uint64, _ int) {
	s.mu.Lock()
	s.added += int(last-first) + 1
	s.mu.Unlock()
}
func (s *recSink) State(st SourceState, msg string) {
	s.mu.Lock()
	s.states = append(s.states, st.String())
	s.mu.Unlock()
}
func (s *recSink) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.added }
func (s *recSink) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.states) == 0 {
		return ""
	}
	return s.states[len(s.states)-1]
}

func newRing() *engine.RingStore { return engine.NewRingStore(10000, 0, &engine.Stats{}) }

func TestTailerLineBacklog(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "2026-10-15 10:00:%02d INFO line %d\n", i%60, i)
	}
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte(b.String()), 0o644)
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	for n, want := range map[int]int{10: 10, 0: 0, 500: 100} {
		ring := newRing()
		sink := &recSink{}
		tl, err := NewTailer(TailOptions{Path: p, SourceID: "a", Registry: reg, Store: ring, Sink: sink, Backlog: 1 << 30,
			BacklogLines: n, UseBacklogLines: true, ForcePoll: true, IdleFlush: 40 * time.Millisecond, PollInterval: 20 * time.Millisecond, Coalesce: 5 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { tl.Run(ctx); close(done) }()
		time.Sleep(250 * time.Millisecond)
		cancel()
		<-done
		if ring.Len() != want {
			t.Errorf("-n %d loaded %d records, want %d", n, ring.Len(), want)
		}
		if n == 10 {
			if first, _ := ring.Get(ring.OldestID()); !strings.Contains(first.Raw.Data, "line 90") {
				t.Errorf("-n 10 should start at line 90, started at %q", first.Raw.Data)
			}
		}
	}
}

func TestStreamIngestsAPipe(t *testing.T) {
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	ring := newRing()
	sink := &recSink{}
	pr, pw := io.Pipe()
	tl, err := NewTailer(TailOptions{SourceID: "stdin", Registry: reg, Store: ring, Sink: sink, Reader: pr,
		IdleFlush: 60 * time.Millisecond, Coalesce: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// a line split across two writes, then a multi-line record, then a final line with no newline
	pw.Write([]byte("2026-10-15 10:00:00 INFO first li"))
	time.Sleep(30 * time.Millisecond)
	pw.Write([]byte("ne\n2026-10-15 10:00:01 ERROR boom\n\tat A.b(A.java:1)\n"))
	waitFor(t, "first record", func() bool { return sink.count() >= 1 })
	waitFor(t, "stack trace held then flushed by the idle timer", func() bool { return sink.count() == 2 })
	pw.Write([]byte("2026-10-15 10:00:02 INFO last line without newline"))
	pw.Close()
	waitFor(t, "end of input", func() bool { return sink.last() == "ended" })
	if ring.Len() != 3 {
		t.Fatalf("expected 3 records, got %d", ring.Len())
	}
	r1, _ := ring.Get(ring.OldestID())
	if !strings.Contains(r1.Raw.Data, "first line") {
		t.Errorf("a line split across reads must be reassembled: %q", r1.Raw.Data)
	}
	r2, _ := ring.Get(ring.OldestID() + 1)
	if !strings.Contains(r2.Raw.Data, "boom") || !strings.Contains(r2.Raw.Data, "A.java:1") {
		t.Errorf("the stack trace belongs to the ERROR record: %q", r2.Raw.Data)
	}
	if r2.Severity != engine.SevError {
		t.Errorf("severity should be parsed from a stream like from a file, got %v", r2.Severity)
	}
}

func TestStreamDetectsJSONAndKeepsRunningAfterEOF(t *testing.T) {
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	ring := newRing()
	sink := &recSink{}
	tl, _ := NewTailer(TailOptions{SourceID: "stdin", Registry: reg, Store: ring, Sink: sink,
		Reader: strings.NewReader(`{"time":"2026-10-15T08:00:00Z","level":"warn","msg":"disk low"}` + "\n" + `{"time":"2026-10-15T08:00:01Z","level":"error","msg":"disk gone"}` + "\n")})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tl.Run(ctx) }()
	waitFor(t, "end of input", func() bool { return sink.last() == "ended" })
	if ring.Len() != 2 {
		t.Fatalf("got %d records", ring.Len())
	}
	if tl.Profile() == nil || tl.Profile().Name != "json" {
		t.Fatalf("a JSON stream should be detected as json, got %v", tl.Profile())
	}
	select {
	case <-done:
		t.Fatal("the view must stay open after the stream ends")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestStateStringsCoverEveryState(t *testing.T) {
	for s := StateRunning; s <= StateEnded; s++ {
		if s.String() == "" {
			t.Errorf("state %d has no name", int(s))
		}
	}
}
