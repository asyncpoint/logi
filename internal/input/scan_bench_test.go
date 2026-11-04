package input

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/filter"
	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/search"
)

// benchStore builds a ~25 MB indexed static file. Cost is paid once per
// benchmark binary run via b.ResetTimer.
func benchStore(b *testing.B, workers int) (*StaticStore, int64) {
	b.Helper()
	var sb strings.Builder
	for i := 0; i < 250000; i++ {
		fmt.Fprintf(&sb, "2026-10-15 10:%02d:%02d %s request %d handled path=/api/v1/items/%d\n",
			(i/60)%60, i%60, []string{"INFO", "WARN", "ERROR", "DEBUG"}[i%4], i, i%977)
	}
	p := filepath.Join(b.TempDir(), "bench.log")
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	sample := []byte(sb.String()[:64<<10])
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	st, err := OpenStatic(StaticOptions{Path: p, SourceID: "bench", Profile: reg.Detect(sample), Sample: sample, ScanWorkers: workers})
	if err != nil {
		b.Fatal(err)
	}
	if err := st.Index(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	return st, int64(sb.Len())
}

// Full parse of every record: the baseline cost that a raw prefilter avoids.
func BenchmarkStaticRangeParseAll(b *testing.B) {
	st, size := benchStore(b, 1)
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.Range(0, 0, func(*engine.LogRecord) bool { return true })
	}
}

// A search that rarely matches: rejected on raw text, never parsed.
func BenchmarkStaticSearchRawPrefilter(b *testing.B) {
	st, size := benchStore(b, 1)
	m, _ := search.Compile(search.Request{Query: "no-such-needle"})
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.RangeRaw(0, 0, m.RawKeep(), func(r *engine.LogRecord) bool { m.Match(r); return true })
	}
}

// Same search with the prefilter disabled, for comparison.
func BenchmarkStaticSearchNoPrefilter(b *testing.B) {
	st, size := benchStore(b, 1)
	m, _ := search.Compile(search.Request{Query: "no-such-needle"})
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.Range(0, 0, func(r *engine.LogRecord) bool { m.Match(r); return true })
	}
}

// Parallel scan (meaningful only with GOMAXPROCS > 1).
func BenchmarkStaticSearchParallel4(b *testing.B) {
	st, size := benchStore(b, 4)
	m, _ := search.Compile(search.Request{Query: "no-such-needle"})
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.RangeRaw(0, 0, m.RawKeep(), func(r *engine.LogRecord) bool { m.Match(r); return true })
	}
}

// Scaling sweeps: how throughput changes with the number of scan workers. Run
// on a multi-core machine; with GOMAXPROCS=1 every row is the sequential path.
//
//	go test ./internal/input -run '^$' -bench 'Scaling' -benchmem
func BenchmarkStaticSearchScaling(b *testing.B) {
	for _, w := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			st, size := benchStore(b, w)
			m, _ := search.Compile(search.Request{Query: "no-such-needle"})
			b.SetBytes(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				st.RangeRaw(0, 0, m.RawKeep(), func(r *engine.LogRecord) bool { m.Match(r); return true })
			}
		})
	}
}

// Parse-bound scan: every record is parsed and tested by a parsed-field
// predicate (severity) that never matches, so output stays tiny and the work is
// dominated by parsing, which is what parallel workers can speed up.
func BenchmarkStaticParseSparseScaling(b *testing.B) {
	pred, _ := filter.Compile("severity:fatal")
	for _, w := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			st, size := benchStore(b, w)
			b.SetBytes(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				st.RangeMatching(context.Background(), 0, 0, nil, pred.Match, func(*engine.LogRecord) bool { return true })
			}
		})
	}
}

// Dense delivery: a predicate that accepts every record (think `-debug` or
// `severity:>=info`), so nearly all parsed records are handed to the caller in
// order. This is the case where buffering for ordered delivery could outweigh
// the parallel speedup; it should be at least as fast as workers=1.
func BenchmarkStaticParseDenseScaling(b *testing.B) {
	all := func(*engine.LogRecord) bool { return true }
	for _, w := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			st, size := benchStore(b, w)
			b.SetBytes(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				st.RangeMatching(context.Background(), 0, 0, nil, all, func(*engine.LogRecord) bool { return true })
			}
		})
	}
}

// One redraw of a screen of scattered filter matches (one block per row).
func BenchmarkStaticScatteredRedraw(b *testing.B) {
	st, _ := benchStore(b, 1)
	ids := make([]uint64, 50)
	for i := range ids {
		ids[i] = uint64(1000 + i*4000)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, id := range ids {
			st.Get(id)
		}
	}
}
