package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/search"
)

func waitCounted(t *testing.T, s *Session) Status {
	t.Helper()
	var st Status
	waitFor(t, "severity counts", func() bool { st = s.Status(); return st.Counts && !st.Counting })
	return st
}

// A static file's severity counts match its content exactly. logText has an ERROR
// on every tenth record and a WARN on every tenth record from the fifth.
func TestStaticSeverityCountsAreExact(t *testing.T) {
	s := newSession(t, logText(500), nil)
	st := waitCounted(t, s)
	if got := st.Stats.BySeverity[engine.SevError]; got != 50 {
		t.Errorf("errors = %d, want 50", got)
	}
	if got := st.Stats.BySeverity[engine.SevWarn]; got != 50 {
		t.Errorf("warnings = %d, want 50", got)
	}
	if got := st.Stats.BySeverity[engine.SevInfo]; got != 400 {
		t.Errorf("info = %d, want 400", got)
	}
	var total int64
	for _, n := range st.Stats.BySeverity {
		total += n
	}
	if total != 500 {
		t.Errorf("counted %d records, want 500", total)
	}
}

// A file much larger than one scan chunk is counted exactly, whether the scan
// runs behind the index or after it, and nothing is counted twice.
func TestStaticSeverityCountsOfALargeFile(t *testing.T) {
	var b strings.Builder
	const n = 200000 // three chunks and a remainder
	for i := 1; i <= n; i++ {
		lvl := "INFO"
		switch {
		case i%100 == 0:
			lvl = "ERROR"
		case i%100 == 50:
			lvl = "WARN"
		case i%1000 == 1:
			lvl = "FATAL"
		}
		fmt.Fprintf(&b, "2026-09-19 10:00:00 %s record %d\n", lvl, i)
	}
	s := newSession(t, b.String(), nil)
	st := waitCounted(t, s)
	want := map[engine.Severity]int64{engine.SevError: n / 100, engine.SevWarn: n / 100, engine.SevFatal: n / 1000}
	for sev, w := range want {
		if got := st.Stats.BySeverity[sev]; got != w {
			t.Errorf("%v = %d, want %d", sev, got, w)
		}
	}
	var total int64
	for _, c := range st.Stats.BySeverity {
		total += c
	}
	if total != n {
		t.Errorf("counted %d records, want %d", total, n)
	}
}

func TestStaticSeverityCountsCanBeSwitchedOff(t *testing.T) {
	s := newSession(t, logText(100), func(c *config.Config) { c.StaticCounts = false })
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	if st := s.Status(); st.Counts || st.Counting {
		t.Fatalf("counts reported although switched off: %+v", st)
	}
}

// Live mode keeps its own counters; the static census does not exist there.
func TestLiveModeHasNoCensus(t *testing.T) {
	s := newSession(t, logText(100), func(c *config.Config) { c.Follow = true })
	waitFor(t, "records", func() bool { return s.Status().Records == 100 })
	if s.Status().Counts {
		t.Fatal("live mode must not report static counts")
	}
	if s.Status().Stats.BySeverity[engine.SevError] != 10 {
		t.Fatalf("live counters: %+v", s.Status().Stats.BySeverity)
	}
}

// ClearSearch removes the matcher (and with it the highlighting) and makes the
// result of a search that was still running recognisable as stale.
func TestClearSearchDropsTheMatcher(t *testing.T) {
	s := newSession(t, logText(100), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	gen, err := s.Search(search.Request{Query: "message"}, 1, Forward, true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Matcher() == nil {
		t.Fatal("searching must install a matcher")
	}
	s.ClearSearch()
	if s.Matcher() != nil {
		t.Fatal("ClearSearch must remove the matcher")
	}
	gen2, err := s.Search(search.Request{Query: "message"}, 1, Forward, true)
	if err != nil {
		t.Fatal(err)
	}
	if gen2 <= gen+1 {
		t.Fatalf("generation did not move past the cleared search: %d then %d", gen, gen2)
	}
}
