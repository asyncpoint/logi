package input

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/filter"
	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/search"
)

func openTemp(t *testing.T, n int) *StaticStore { return openTempW(t, n, 1) }

func openTempW(t *testing.T, n, workers int) *StaticStore {
	var b strings.Builder
	for i := 0; i < n; i++ {
		lvl := []string{"INFO", "WARN", "ERROR", "DEBUG"}[i%4]
		fmt.Fprintf(&b, "2026-10-15 10:%02d:%02d %s request %d handled\n", (i/60)%60, i%60, lvl, i)
		if i%97 == 0 {
			b.WriteString("\tat Stack.Trace(Foo.java:12) NEEDLE\n")
		}
	}
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte(b.String()), 0o644)
	sample := []byte(b.String())
	if len(sample) > 64<<10 {
		sample = sample[:64<<10]
	}
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	st, err := OpenStatic(StaticOptions{Path: p, SourceID: "a", Profile: reg.Detect(sample), Sample: sample, ScanWorkers: workers})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// RangeRaw with a necessary-condition prefilter must yield exactly the matches
// of a full Range + predicate, including IDs and bounds.
func TestRangeRawEquivalentToRange(t *testing.T) {
	st := openTemp(t, 20000)
	defer st.Close()
	for _, q := range []string{"needle", "request 1234 ", "error", "zzz", "handled"} {
		m, _ := search.Compile(search.Request{Query: q})
		for _, b := range [][2]uint64{{0, 0}, {1, 500}, {130, 9000}, {19990, 0}, {777, 777}} {
			var want, got []uint64
			st.Range(b[0], b[1], func(r *engine.LogRecord) bool {
				if m.Match(r) {
					want = append(want, r.ID)
				}
				return true
			})
			if b[0] != 0 || b[1] != 0 { // recompute under explicit bounds
				want = want[:0]
				st.Range(b[0], b[1], func(r *engine.LogRecord) bool {
					if m.Match(r) {
						want = append(want, r.ID)
					}
					return true
				})
			}
			st.RangeRaw(b[0], b[1], m.RawKeep(), func(r *engine.LogRecord) bool {
				if m.Match(r) {
					got = append(got, r.ID)
				}
				return true
			})
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("q=%q bounds=%v: raw=%v want=%v", q, b, got, want)
			}
		}
	}
	// filter prefilter: raw term + parsed term
	p, _ := filter.Compile("request severity:>=warn")
	var want, got []uint64
	st.Range(0, 0, func(r *engine.LogRecord) bool {
		if p.Match(r) {
			want = append(want, r.ID)
		}
		return true
	})
	st.RangeRaw(0, 0, filter.RawPrefilter(p), func(r *engine.LogRecord) bool {
		if p.Match(r) {
			got = append(got, r.ID)
		}
		return true
	})
	if len(want) == 0 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("filter mismatch: %d vs %d", len(got), len(want))
	}
}

func TestRangeRawStopsAtUpperBound(t *testing.T) {
	st := openTemp(t, 50000)
	defer st.Close()
	calls := 0
	st.RangeRaw(1, 100, func(string) bool { calls++; return false }, func(*engine.LogRecord) bool { return true })
	if calls > 100+1 {
		t.Fatalf("scanned %d records past upper bound 100", calls)
	}
}

// Parallel Range must deliver exactly what the sequential one does, in order,
// honour bounds and early stop, and leave no goroutines behind.
func TestParallelRangeMatchesSequential(t *testing.T) {
	seq := openTempW(t, 120000, 1) // ~6 MiB => several segments
	par := openTempW(t, 120000, 4)
	defer seq.Close()
	defer par.Close()
	collect := func(st *StaticStore, from, to uint64, keep func(string) bool, limit int) []string {
		var out []string
		st.RangeRaw(from, to, keep, func(r *engine.LogRecord) bool {
			out = append(out, fmt.Sprintf("%d@%d", r.ID, r.Raw.Offset))
			return limit == 0 || len(out) < limit
		})
		return out
	}
	needle := func(s string) bool { return strings.Contains(s, "NEEDLE") }
	before := runtime.NumGoroutine()
	for _, c := range []struct {
		from, to uint64
		keep     func(string) bool
		limit    int
	}{
		{0, 0, nil, 0}, {0, 0, needle, 0}, {5000, 90000, nil, 0}, {7, 7, nil, 0},
		{0, 0, nil, 10}, {30000, 0, needle, 3}, {119990, 0, nil, 0},
	} {
		a, b := collect(seq, c.from, c.to, c.keep, c.limit), collect(par, c.from, c.to, c.keep, c.limit)
		if len(a) == 0 || strings.Join(a, ",") != strings.Join(b, ",") {
			t.Fatalf("case %+v: sequential %d recs vs parallel %d recs differ", c, len(a), len(b))
		}
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutine leak: %d -> %d", before, after)
	}
}

// The detail view reads a record synchronously on the UI goroutine, so a huge
// multi-line record must be clipped (with a visible marker), not read whole.
func TestRawIsClippedForHugeRecords(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ { // >=20% of the first 256 lines must be timestamped for multiline framing
		fmt.Fprintf(&b, "2026-10-15 09:%02d:00 INFO warmup %d\n", i%60, i)
	}
	b.WriteString("2026-10-15 10:00:00 ERROR giant stack\n")
	line := strings.Repeat("x", 58) + "\n"
	for b.Len() < 6<<20 {
		b.WriteString(line)
	}
	b.WriteString("2026-10-15 10:00:01 INFO after\n")
	p := filepath.Join(t.TempDir(), "g.log")
	os.WriteFile(p, []byte(b.String()), 0o644)
	sample := []byte(b.String()[:64<<10])
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	st, err := OpenStatic(StaticOptions{Path: p, SourceID: "g", Profile: reg.Detect(sample), Sample: sample})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, ok := st.Get(101)
	if !ok || rec.Raw.Length < 5<<20 {
		t.Fatalf("expected a >5MiB record, got ok=%v len=%d", ok, rec.Raw.Length)
	}
	raw, err := st.Raw(&rec)
	if err != nil || len(raw) > maxRawBytes+256 || !strings.Contains(raw, "record continues for") {
		t.Fatalf("len=%d err=%v clipped-marker=%v", len(raw), err, strings.Contains(raw, "record continues"))
	}
	// a normal record is returned untouched
	rec2, _ := st.Get(102)
	if raw2, _ := st.Raw(&rec2); !strings.HasPrefix(raw2, "2026-10-15 10:00:01 INFO after") || strings.Contains(raw2, "continues") {
		t.Fatalf("normal record altered: %q", raw2)
	}
}

// A scan whose records are all rejected before reaching the callback must still
// stop promptly when its context is cancelled (otherwise a cancelled search over
// a huge file would run to the end and block shutdown).
func TestRangeMatchingCancelsWhileRejectingEverything(t *testing.T) {
	for _, workers := range []int{1, 4} {
		st := openTempW(t, 120000, workers)
		ctx, cancel := context.WithCancel(context.Background())
		slow := func(string) bool { time.Sleep(20 * time.Microsecond); return false } // ~2.4s+ if never cancelled
		done := make(chan struct{})
		go func() {
			st.RangeMatching(ctx, 0, 0, slow, nil, func(*engine.LogRecord) bool { return true })
			close(done)
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatalf("workers=%d: scan did not stop after cancel", workers)
		}
		st.Close()
	}
}

// accept (the parsed-record predicate) runs inside the scan; sequential and
// parallel results must be identical, and match a plain Range + filter.
func TestRangeMatchingAcceptEquivalence(t *testing.T) {
	seq := openTempW(t, 120000, 1)
	par := openTempW(t, 120000, 4)
	defer seq.Close()
	defer par.Close()
	pred, err := filter.Compile("severity:>=warn")
	if err != nil {
		t.Fatal(err)
	}
	var want []uint64
	seq.Range(0, 0, func(r *engine.LogRecord) bool {
		if pred.Match(r) {
			want = append(want, r.ID)
		}
		return true
	})
	for name, st := range map[string]*StaticStore{"sequential": seq, "parallel": par} {
		var got []uint64
		st.RangeMatching(context.Background(), 0, 0, nil, pred.Match, func(r *engine.LogRecord) bool {
			got = append(got, r.ID)
			return true
		})
		if len(want) == 0 || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: %d matches, want %d", name, len(got), len(want))
		}
	}
}

// Unicode case-insensitive search must find the same records whether it runs
// through the raw prefilter (sequential or parallel) or a plain scan.
func TestUnicodeSearchThroughPrefilter(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 60000; i++ {
		msg := "request handled"
		switch {
		case i%501 == 0:
			msg = "Fehler in der ÉCOLE-Verbindung"
		case i%777 == 0:
			msg = "erreur école fermée"
		}
		fmt.Fprintf(&b, "2026-10-15 10:%02d:%02d INFO %s %d\n", (i/60)%60, i%60, msg, i)
	}
	p := filepath.Join(t.TempDir(), "u.log")
	os.WriteFile(p, []byte(b.String()), 0o644)
	sample := []byte(b.String()[:64<<10])
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	m, _ := search.Compile(search.Request{Query: "École"})
	var want []uint64
	for _, workers := range []int{1, 4} {
		st, err := OpenStatic(StaticOptions{Path: p, SourceID: "u", Profile: reg.Detect(sample), Sample: sample, ScanWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Index(context.Background()); err != nil {
			t.Fatal(err)
		}
		if workers == 1 {
			st.Range(0, 0, func(r *engine.LogRecord) bool {
				if m.Match(r) {
					want = append(want, r.ID)
				}
				return true
			})
		}
		var got []uint64
		st.RangeMatching(context.Background(), 0, 0, m.RawKeep(), m.Match, func(r *engine.LogRecord) bool {
			got = append(got, r.ID)
			return true
		})
		if len(want) < 100 || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("workers=%d: %d matches via prefilter, %d via plain scan", workers, len(got), len(want))
		}
		st.Close()
	}
}

// Coloured logs (Spring Boot, Node, pytest ...) must parse like plain ones, and a
// search must find them: the prefilter sees the same stripped text as the matcher.
func TestColouredLogsParseAndSearch(t *testing.T) {
	const esc = "\x1b"
	var b strings.Builder
	for i := 0; i < 40; i++ {
		lvl := esc + "[32m INFO" + esc + "[0;39m"
		if i%10 == 0 {
			lvl = esc + "[1;31mERROR" + esc + "[0;39m"
		}
		fmt.Fprintf(&b, "%s[2m2026-10-15 10:00:%02d%s[0;39m %s request %d handled\n", esc, i, esc, lvl, i)
	}
	p := filepath.Join(t.TempDir(), "color.log")
	os.WriteFile(p, []byte(b.String()), 0o644)
	sample := []byte(b.String())
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	st, err := OpenStatic(StaticOptions{Path: p, SourceID: "c", Profile: reg.Detect(sample), Sample: sample})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, ok := st.Get(1)
	if !ok || rec.Timestamp.IsZero() || rec.Severity != engine.SevError || strings.ContainsRune(rec.Raw.Data, 0x1b) {
		t.Fatalf("a coloured record should parse cleanly: ts=%v sev=%v data=%q", rec.Timestamp, rec.Severity, rec.Raw.Data)
	}
	m, _ := search.Compile(search.Request{Query: "ERROR request 10"})
	var got []uint64
	st.RangeMatching(context.Background(), 0, 0, m.RawKeep(), m.Match, func(r *engine.LogRecord) bool {
		got = append(got, r.ID)
		return true
	})
	if len(got) != 1 || got[0] != 11 {
		t.Fatalf("search across colour codes found %v, want [11]", got)
	}
}
