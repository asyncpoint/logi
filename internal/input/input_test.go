package input

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/parser"
)

func registry(t testing.TB) *parser.Registry {
	r := parser.NewRegistry()
	if err := parser.RegisterCore(r); err != nil {
		t.Fatal(err)
	}
	return r
}

// ---- static store ------------------------------------------------------

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openStatic(t testing.TB, path string, block int) *StaticStore {
	t.Helper()
	data, _ := os.ReadFile(path)
	if len(data) > 4096 {
		data = data[:4096]
	}
	prof := registry(t).Detect(data)
	s, err := OpenStatic(StaticOptions{Path: path, SourceID: "t", Profile: prof, Sample: data, BlockSize: block, CacheBlocks: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func genLog(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "2026-09-19 11:%02d:%02d INFO record number %d\n", (i/60)%60, i%60, i)
		if i%7 == 0 {
			b.WriteString("java.lang.Exception: boom\n   at a.b.C()\n")
		}
	}
	return b.String()
}

func TestStaticStoreRandomAccessAndRange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	const n = 1000
	writeFile(t, p, genLog(n))
	s := openStatic(t, p, 10)
	if s.Len() != n || s.OldestID() != 1 || s.NewestID() != n {
		t.Fatalf("len=%d", s.Len())
	}
	for _, id := range []uint64{1, 7, 8, 500, 999, n} {
		r, ok := s.Get(id)
		if !ok || r.ID != id || !strings.Contains(r.Message, fmt.Sprintf("record number %d", id)) {
			t.Fatalf("Get(%d) = %+v %v", id, r, ok)
		}
	}
	if r, _ := s.Get(7); !r.Multiline || !strings.Contains(r.Message, "at a.b.C()") || r.LineEnd-r.LineStart != 2 {
		t.Fatalf("multiline record wrong: %+v", r)
	}
	if _, ok := s.Get(n + 1); ok {
		t.Fatal("Get past end must fail")
	}
	// Range over everything is ordered and complete.
	var ids []uint64
	s.Range(0, 0, func(r *engine.LogRecord) bool { ids = append(ids, r.ID); return true })
	if len(ids) != n || ids[0] != 1 || ids[n-1] != n {
		t.Fatalf("range yielded %d ids", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] != ids[i-1]+1 {
			t.Fatalf("gap at %d", i)
		}
	}
	// Sub-range crossing block boundaries + early stop.
	ids = ids[:0]
	s.Range(95, 130, func(r *engine.LogRecord) bool { ids = append(ids, r.ID); return true })
	if len(ids) != 36 || ids[0] != 95 || ids[35] != 130 {
		t.Fatalf("subrange: %v", ids)
	}
	c := 0
	s.Range(1, 0, func(*engine.LogRecord) bool { c++; return c < 5 })
	if c != 5 {
		t.Fatalf("early stop c=%d", c)
	}
	// Cached static records keep semantic data but not the original payload.
	// The raw text remains available through the store's lazy Raw accessor.
	r, _ := s.Get(7)
	if r.Raw.Data != "" {
		t.Fatalf("static cache retained raw payload: %d bytes", len(r.Raw.Data))
	}
	raw, err := s.Raw(&r)
	if err != nil || !strings.HasPrefix(raw, "2026-09-19") || !strings.HasSuffix(raw, "at a.b.C()") {
		t.Fatalf("raw=%q err=%v", raw, err)
	}

	pos, ok := s.Position(7)
	if !ok || pos.Kind != engine.PositionStaticOffset || pos.RecordID != 7 || pos.Offset < 0 || pos.Length <= 0 {
		t.Fatalf("invalid static position: %#v, ok=%v", pos, ok)
	}
	resolved, ok := s.ResolvePosition(pos)
	if !ok || resolved.ID != 7 || resolved.Raw.Offset != pos.Offset || resolved.Raw.Length != pos.Length {
		t.Fatalf("position did not resolve: %#v, ok=%v", resolved, ok)
	}
	wrongSource := pos
	wrongSource.SourceID = "other.log"
	if _, ok := s.ResolvePosition(wrongSource); ok {
		t.Fatal("position from another source should not resolve")
	}
	wrongOffset := pos
	wrongOffset.Offset++
	if _, ok := s.ResolvePosition(wrongOffset); ok {
		t.Fatal("position with mismatched offset should not resolve")
	}
}

func TestStaticRangeStreamsOversizedCheckpointBlock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "large-block.log")
	var data strings.Builder
	const records = 96
	payload := strings.Repeat("x", 32<<10)
	for i := 0; i < records; i++ {
		fmt.Fprintf(&data, "record-%03d %s\n", i+1, payload)
	}
	writeFile(t, p, data.String())
	sample := []byte(data.String()[:4096])
	prof := registry(t).Detect(sample)
	s, err := OpenStatic(StaticOptions{Path: p, SourceID: "large", Profile: prof, Sample: sample, BlockSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Len() != records {
		t.Fatalf("indexed %d records, want %d", s.Len(), records)
	}
	got := 0
	s.Range(1, 0, func(r *engine.LogRecord) bool {
		got++
		if r.ID != uint64(got) {
			t.Fatalf("record ID %d at position %d", r.ID, got)
		}
		return true
	})
	if got != records {
		t.Fatalf("range returned %d records, want %d", got, records)
	}
}

func TestStaticCacheHonorsByteBudget(t *testing.T) {
	p := filepath.Join(t.TempDir(), "budget.log")
	writeFile(t, p, genLog(4))
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	prof := registry(t).Detect(data)
	s, err := OpenStatic(StaticOptions{Path: p, SourceID: "budget", Profile: prof, Sample: data,
		BlockSize: 2, CacheBlocks: 8, CacheBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r, ok := s.Get(1); !ok || r.ID != 1 {
		t.Fatalf("oversized uncached block could not be read: record=%+v ok=%v", r, ok)
	}
	if len(s.cache) != 0 || s.cacheBytes != 0 {
		t.Fatalf("cache exceeded its one-byte budget: blocks=%d bytes=%d", len(s.cache), s.cacheBytes)
	}
}

func TestStaticCacheLRUTouchDoesNotDuplicateEntries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.log")
	writeFile(t, p, genLog(30))
	s := openStatic(t, p, 10)
	for _, id := range []uint64{1, 11, 21, 1} {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("Get(%d) failed", id)
		}
	}
	if len(s.order) != 3 {
		t.Fatalf("LRU entries=%v, want three unique blocks", s.order)
	}
	want := []uint64{1, 2, 0}
	for i := range want {
		if s.order[i] != want[i] {
			t.Fatalf("LRU order=%v, want %v", s.order, want)
		}
	}
	seen := map[uint64]bool{}
	for _, b := range s.order {
		if seen[b] || s.cache[b] == nil {
			t.Fatalf("duplicate or missing cache entry: order=%v cache=%v", s.order, s.cache)
		}
		seen[b] = true
	}
}

func TestStaticStoreEdgeCases(t *testing.T) {
	dir := t.TempDir()
	e := filepath.Join(dir, "empty.log")
	writeFile(t, e, "")
	if s := openStatic(t, e, 4); s.Len() != 0 || s.OldestID() != 0 {
		t.Fatal("empty file")
	}
	n := filepath.Join(dir, "nonl.log")
	writeFile(t, n, "line one\r\nline two\r\n\r\nlast without newline")
	s := openStatic(t, n, 2)
	if s.Len() != 3 {
		t.Fatalf("len=%d", s.Len())
	}
	if r, _ := s.Get(3); r.Message != "last without newline" {
		t.Fatalf("%q", r.Message)
	}
	if r, _ := s.Get(1); r.Message != "line one" {
		t.Fatalf("crlf: %q", r.Message)
	}
}

func TestStaticIndexIsCancelable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.log")
	writeFile(t, p, genLog(20000))
	data, _ := os.ReadFile(p)
	prof := registry(t).Detect(data[:4096])
	s, _ := OpenStatic(StaticOptions{Path: p, Profile: prof, Sample: data[:4096]})
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Index(ctx); err == nil {
		t.Fatal("expected cancellation error")
	}
}

// ---- tailer --------------------------------------------------------------

type testSink struct {
	mu     sync.Mutex
	states []SourceState
	added  int
}

func (s *testSink) Added(first, last uint64, evicted int) {
	s.mu.Lock()
	s.added += int(last-first) + 1
	s.mu.Unlock()
}
func (s *testSink) State(st SourceState, _ string) {
	s.mu.Lock()
	s.states = append(s.states, st)
	s.mu.Unlock()
}
func (s *testSink) has(st SourceState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.states {
		if x == st {
			return true
		}
	}
	return false
}

type harness struct {
	ring *engine.RingStore
	sink *testSink
	stop func()
	path string
}

func startTail(t *testing.T, path string, mod func(*TailOptions)) *harness {
	t.Helper()
	h := &harness{ring: engine.NewRingStore(10000, 0, &engine.Stats{}), sink: &testSink{}, path: path}
	o := TailOptions{Path: path, SourceID: "t", Registry: registry(t), Store: h.ring, Sink: h.sink,
		Backlog: -1, IdleFlush: 60 * time.Millisecond, Coalesce: time.Millisecond, SafetyInterval: 100 * time.Millisecond, PollInterval: 20 * time.Millisecond}
	if mod != nil {
		mod(&o)
	}
	tl, err := NewTailer(o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()
	h.stop = func() { cancel(); <-done }
	t.Cleanup(h.stop)
	return h
}

func (h *harness) texts() (out []string) {
	h.ring.Range(0, 0, func(r *engine.LogRecord) bool { out = append(out, r.Message); return true })
	return
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func TestTailAppendsIncrementally(t *testing.T) {
	p := filepath.Join(t.TempDir(), "live.log")
	writeFile(t, p, "2026-09-19 10:00:00 INFO one\n2026-09-19 10:00:01 INFO two\n")
	h := startTail(t, p, nil)
	waitFor(t, "initial backlog", func() bool { return h.ring.Len() == 2 })
	appendTo(t, p, "2026-09-19 10:00:02 WARN three\n2026-09-19 10:00:03 ERROR four\n")
	waitFor(t, "appended", func() bool { return h.ring.Len() == 4 })
	if got := h.texts(); got[3] != "four" || got[2] != "three" {
		t.Fatalf("%v", got)
	}
	r, _ := h.ring.Get(4)
	if r.Severity != engine.SevError || r.Raw.Offset == 0 {
		t.Fatalf("%+v", r)
	}
}

func TestTailPartialLineIsNotEmittedEarly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.log")
	writeFile(t, p, "first line\n")
	h := startTail(t, p, func(o *TailOptions) { o.Parser = "generic" })
	waitFor(t, "first", func() bool { return h.ring.Len() == 1 })
	appendTo(t, p, "2026-09-18 INFO Request sta")
	time.Sleep(200 * time.Millisecond)
	if h.ring.Len() != 1 {
		t.Fatalf("partial line was emitted: %v", h.texts())
	}
	appendTo(t, p, "rted successfully\n")
	waitFor(t, "completed record", func() bool { return h.ring.Len() == 2 })
	if got := h.texts()[1]; got != "Request started successfully" {
		t.Fatalf("got %q", got)
	}
}

func TestTailMultilineIdleFlush(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.log")
	writeFile(t, p, "2026-09-19 10:00:00 INFO ok\n2026-09-19 10:00:01 ERROR failed\nSystem.Exception: x\n   at A()\n")
	h := startTail(t, p, nil)
	// The ERROR record has no successor line, so only the idle flush can release it.
	waitFor(t, "both records", func() bool { return h.ring.Len() == 2 })
	r, _ := h.ring.Get(2)
	if !r.Multiline || !strings.Contains(r.Message, "at A()") {
		t.Fatalf("%+v", r)
	}
}

func TestTailRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.log")
	writeFile(t, p, "2026-09-19 10:00:00 INFO A1\n")
	h := startTail(t, p, nil)
	waitFor(t, "A1", func() bool { return h.ring.Len() == 1 })
	appendTo(t, p, "2026-09-19 10:00:01 INFO A2\n")
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "2026-09-19 10:00:02 INFO B1\n")
	waitFor(t, "B1 after rotation", func() bool { return h.ring.Len() == 3 })
	if got := strings.Join(h.texts(), ","); got != "A1,A2,B1" {
		t.Fatalf("got %s", got)
	}
	if !h.sink.has(StateRotated) {
		t.Fatal("rotation state not reported")
	}
	appendTo(t, p, "2026-09-19 10:00:03 INFO B2\n")
	waitFor(t, "B2", func() bool { return h.ring.Len() == 4 })
}

func TestTailTruncation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.log")
	writeFile(t, p, "2026-09-19 10:00:00 INFO old-1\n2026-09-19 10:00:01 INFO old-2\n2026-09-19 10:00:02 INFO old-3\n")
	h := startTail(t, p, nil)
	waitFor(t, "old", func() bool { return h.ring.Len() == 3 })
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	appendTo(t, p, "2026-09-19 11:00:00 INFO new-1\n")
	waitFor(t, "new after truncate", func() bool { return h.ring.Len() == 4 })
	if !h.sink.has(StateTruncated) {
		t.Fatal("truncation not reported")
	}
}

func TestTailWaitsForMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "later.log")
	h := startTail(t, p, nil)
	waitFor(t, "waiting state", func() bool { return h.sink.has(StateWaiting) })
	writeFile(t, p, "2026-09-19 10:00:00 INFO hello\n")
	waitFor(t, "record after creation", func() bool { return h.ring.Len() == 1 })
}

func TestTailPollingFallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "poll.log")
	writeFile(t, p, "a\n")
	h := startTail(t, p, func(o *TailOptions) { o.ForcePoll = true; o.Parser = "generic" })
	waitFor(t, "a", func() bool { return h.ring.Len() == 1 })
	appendTo(t, p, "b\n")
	waitFor(t, "b via polling", func() bool { return h.ring.Len() == 2 })
}

func TestTailBacklogOnlyReadsTailOfLargeFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.log")
	writeFile(t, p, genLog(5000))
	st, _ := os.Stat(p)
	h := startTail(t, p, func(o *TailOptions) { o.Backlog = 2048 })
	waitFor(t, "some backlog", func() bool { return h.ring.Len() > 0 })
	time.Sleep(50 * time.Millisecond)
	first, _ := h.ring.Get(h.ring.OldestID())
	if first.Raw.Offset < st.Size()-2048-200 || h.ring.Len() > 60 {
		t.Fatalf("read too much: len=%d firstOffset=%d size=%d", h.ring.Len(), first.Raw.Offset, st.Size())
	}
	if first.Message == "" || !strings.HasPrefix(first.Raw.Data, "2026-") && !strings.HasPrefix(first.Raw.Data, "java") && !strings.HasPrefix(first.Raw.Data, " ") {
		t.Fatalf("backlog must start on a line boundary: %q", first.Raw.Data)
	}
}

func TestTailerLeaksNoGoroutines(t *testing.T) {
	base := runtime.NumGoroutine()
	p := filepath.Join(t.TempDir(), "g.log")
	writeFile(t, p, "x\n")
	for i := 0; i < 5; i++ {
		h := startTail(t, p, func(o *TailOptions) { o.Parser = "generic" })
		waitFor(t, "x", func() bool { return h.ring.Len() == 1 })
		h.stop()
	}
	time.Sleep(100 * time.Millisecond)
	if n := runtime.NumGoroutine(); n > base+2 {
		t.Fatalf("goroutines grew from %d to %d", base, n)
	}
}

func TestTailDoesNotCreateFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "w.log")
	writeFile(t, p, "hello\n")
	before, _ := os.ReadDir(dir)
	h := startTail(t, p, func(o *TailOptions) { o.Parser = "generic" })
	waitFor(t, "hello", func() bool { return h.ring.Len() == 1 })
	h.stop()
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatalf("logi created files: %v", after)
	}
}

func BenchmarkTailIngest(b *testing.B) {
	p := filepath.Join(b.TempDir(), "bench.log")
	var sb strings.Builder
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&sb, "2026-09-19 11:32:04,123 [worker-3] INFO - request %d completed in 42ms\n", i)
	}
	os.WriteFile(p, []byte(sb.String()), 0o644)
	b.SetBytes(int64(sb.Len()))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring := engine.NewRingStore(50000, 0, &engine.Stats{})
		tl, _ := NewTailer(TailOptions{Path: p, Registry: registry(b), Store: ring, Sink: &testSink{}, Backlog: -1, ForcePoll: true, PollInterval: time.Hour})
		ctx, cancel := context.WithCancel(context.Background())
		go tl.Run(ctx)
		for ring.NewestID() < 100000 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}
}

// Regression: with PollInterval < IdleFlush the idle timer used to be re-armed
// on every poll tick and never fired, so a trailing multiline record was never released.
func TestIdleFlushSurvivesFrequentPolling(t *testing.T) {
	p := filepath.Join(t.TempDir(), "idle.log")
	writeFile(t, p, "2026-09-19 10:00:00 ERROR failed\nSystem.Exception: x\n")
	h := startTail(t, p, func(o *TailOptions) {
		o.ForcePoll = true
		o.PollInterval = 10 * time.Millisecond
		o.IdleFlush = 80 * time.Millisecond
	})
	waitFor(t, "idle flush despite 8 poll ticks per flush window", func() bool { return h.ring.Len() == 1 })
	r, _ := h.ring.Get(1)
	if !r.Multiline || !strings.Contains(r.Message, "System.Exception") {
		t.Fatalf("%+v", r)
	}
}

func BenchmarkStaticRangeLargeCheckpointBlock(b *testing.B) {
	p := filepath.Join(b.TempDir(), "large-block.log")
	var data strings.Builder
	const records = 96
	payload := strings.Repeat("x", 32<<10)
	for i := 0; i < records; i++ {
		fmt.Fprintf(&data, "record-%03d %s\n", i+1, payload)
	}
	if err := os.WriteFile(p, []byte(data.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	sample := []byte(data.String()[:4096])
	prof := registry(b).Detect(sample)
	s, err := OpenStatic(StaticOptions{Path: p, SourceID: "benchmark", Profile: prof, Sample: sample, BlockSize: 128})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if err := s.Index(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data.String())))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		s.Range(1, 0, func(*engine.LogRecord) bool { count++; return true })
		if count != records {
			b.Fatalf("range returned %d records, want %d", count, records)
		}
	}
}
