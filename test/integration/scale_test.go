// Package integration holds end-to-end resource tests for the ADR success
// criteria: bounded memory, lazy static access, responsive search.
// Skipped with -short.
package integration

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/search"
)

func heapMB() float64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

func writeLog(t *testing.T, path string, n int, appendMode bool) {
	t.Helper()
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendMode {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	sevs := []string{"INFO", "INFO", "INFO", "WARN", "ERROR", "DEBUG"}
	for i := 0; i < n; i++ {
		fmt.Fprintf(w, "2026-09-19 %02d:%02d:%02d,%03d %s [worker-%d] request %d completed in %dms path=/api/v1/items/%d\n",
			(i/3600)%24, (i/60)%60, i%60, i%1000, sevs[i%len(sevs)], i%16, i, i%900, i)
	}
	w.Flush()
	f.Close()
}

// drain consumes UI updates like the TUI would and remembers the latest search result.
func drain(ctx context.Context, s *app.Session) *atomic.Pointer[app.SearchResult] {
	var last atomic.Pointer[app.SearchResult]
	go func() {
		for {
			select {
			case u := <-s.Updates():
				if u.Search != nil {
					r := *u.Search
					last.Store(&r)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return &last
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

// Live memory is bounded by configuration, not by log volume.
func TestLiveMemoryIsBoundedByRingNotVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	p := filepath.Join(t.TempDir(), "live.log")
	writeLog(t, p, 10, false)
	cfg := config.Defaults()
	cfg.Path, cfg.Follow, cfg.Backlog, cfg.Buffer, cfg.BufferBytes = p, true, -1, 20000, 0
	cfg.BatchInterval = 20 * time.Millisecond
	s, err := app.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); s.Close() }()
	s.Start(ctx)
	drain(ctx, s)
	waitFor(t, "initial", 5*time.Second, func() bool { return s.Status().Records == 10 })

	const total = 1_000_000
	start := time.Now()
	for done := 0; done < total; done += 100_000 {
		writeLog(t, p, 100_000, true)
	}
	waitFor(t, "ingest 1M records", 60*time.Second, func() bool { return s.Status().Stats.Ingested >= total })
	elapsed := time.Since(start)

	st := s.Status()
	if st.Records > 20000 {
		t.Fatalf("ring exceeded capacity: %d", st.Records)
	}
	if st.Stats.Evicted < total-20000 {
		t.Fatalf("expected ~%d evictions, got %d", total-20000, st.Stats.Evicted)
	}
	// Statistics must stay exact through eviction.
	var sum int64
	for _, c := range st.Stats.BySeverity {
		sum += c
	}
	if sum != int64(st.Records) {
		t.Fatalf("severity counters (%d) drifted from ring content (%d)", sum, st.Records)
	}
	mb := heapMB()
	t.Logf("1M records in %v (%.0f rec/s); ring=%d; heap after GC=%.1f MiB", elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds(), st.Records, mb)
	if mb > 64 {
		t.Fatalf("heap %.1f MiB is not bounded by the ring", mb)
	}
}

// Opening a large static file must not load its contents into memory.
func TestStaticLargeFileStaysOnDisk(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	p := filepath.Join(t.TempDir(), "huge.log")
	const n = 3_500_000 // ≈ 340 MB
	writeLog(t, p, n, false)
	fi, _ := os.Stat(p)
	t.Logf("file size: %.0f MiB, %d records", float64(fi.Size())/(1<<20), n)

	cfg := config.Defaults()
	cfg.Path = p
	cfg.BatchInterval = 20 * time.Millisecond
	openStart := time.Now()
	s, err := app.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); s.Close() }()
	s.Start(ctx)
	lastSearch := drain(ctx, s)

	// The first screen is available immediately, before indexing finishes.
	waitFor(t, "first rows", 5*time.Second, func() bool { _, ok := s.ViewAt(0); return ok })
	t.Logf("first record visible after %v (index done: %v)", time.Since(openStart).Round(time.Millisecond), s.Status().IndexDone)

	waitFor(t, "index", 120*time.Second, func() bool { return s.Status().IndexDone })
	t.Logf("full index in %v", time.Since(openStart).Round(time.Millisecond))
	if got := s.ViewLen(); got != n {
		t.Fatalf("indexed %d records, want %d", got, n)
	}
	if mb := heapMB(); mb > 64 {
		t.Fatalf("static mode holds %.1f MiB on heap for a %.0f MiB file", mb, float64(fi.Size())/(1<<20))
	} else {
		t.Logf("heap after indexing: %.1f MiB", mb)
	}

	// Random access is cheap.
	rs := time.Now()
	for _, row := range []int{0, n / 2, n - 1, 123456, 3_000_000} {
		rec, ok := s.ViewAt(row)
		if !ok || rec.ID != uint64(row+1) {
			t.Fatalf("row %d -> %+v %v", row, rec, ok)
		}
	}
	if d := time.Since(rs); d > 500*time.Millisecond {
		t.Fatalf("5 random reads took %v", d)
	}
	last, _ := s.ViewAt(n - 1)
	if want := fmt.Sprintf("request %d completed", n-1); !contains(last.Message, want) {
		t.Fatalf("last record wrong: %q", last.Message)
	}

	// Search near the end of the file scans lazily and stays cancelable.
	ss := time.Now()
	gen, err := s.Search(search.Request{Query: fmt.Sprintf("request %d completed", n-5)}, 1, app.Forward, false)
	if err != nil {
		t.Fatal(err)
	}
	var res *app.SearchResult
	waitFor(t, "search result", 60*time.Second, func() bool {
		if r := lastSearch.Load(); r != nil && r.Gen == gen {
			res = r
			return true
		}
		return false
	})
	if !res.Found || res.ID != uint64(n-4) {
		t.Fatalf("search result %+v", res)
	}
	t.Logf("search across %d records: %v", n, time.Since(ss).Round(time.Millisecond))

	// A filter over the whole file produces a bounded index.
	if err := s.SetFilter("severity:error"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "filter", 120*time.Second, func() bool { st := s.Status(); return !st.Filtering && st.ViewRows > 0 })
	t.Logf("filter severity:error -> %d rows", s.Status().ViewRows)
	if mb := heapMB(); mb > 96 {
		t.Fatalf("heap after filter %.1f MiB", mb)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
