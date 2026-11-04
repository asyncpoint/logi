package app

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// Searching a filtered static view must match the whole record (timestamp and
// level included), exactly like an unfiltered search does.
func TestFilteredStaticSearchMatchesRawText(t *testing.T) {
	s := newSession(t, logText(500), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	if r := runSearch(t, s, "WARN", 1, Forward, false); !r.Found || r.ID != 5 {
		t.Fatalf("unfiltered baseline: %+v", r)
	}
	s.SetFilter("severity:warn")
	waitFor(t, "filter", func() bool { st := s.Status(); return !st.Filtering && st.ViewRows == 50 })
	for _, q := range []string{"WARN", "2026-09-19 10:00:05"} {
		if r := runSearch(t, s, q, 1, Forward, false); !r.Found || r.ID != 5 {
			t.Fatalf("filtered search %q: %+v", q, r)
		}
	}
	if r := runSearch(t, s, "WARN", 500, Backward, false); !r.Found || r.ID != 495 {
		t.Fatalf("filtered backward: %+v", r)
	}
}

// Backward search over a file larger than one scan window must still return the
// nearest match before the cursor.
func TestBackwardSearchAcrossWindows(t *testing.T) {
	pad := strings.Repeat("x", 180)
	var b strings.Builder
	const n = 40000
	for i := 1; i <= n; i++ {
		tag := "plain"
		if i == 3 || i == 21000 {
			tag = "NEEDLE"
		}
		fmt.Fprintf(&b, "2026-09-19 10:00:00 INFO %s %d %s\n", tag, i, pad)
	}
	// The window is about one read segment per scan worker, so on a many-core
	// machine it would cover this whole file. Pin one worker: the test is about
	// searching across window boundaries, not about parallelism.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	s := newSession(t, b.String(), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	if w := s.static.ScanWindow(4096); w >= n/2 {
		t.Fatalf("window %d does not exercise multiple windows", w)
	}
	if r := runSearch(t, s, "NEEDLE", n, Backward, false); !r.Found || r.ID != 21000 {
		t.Fatalf("nearest backward: %+v", r)
	}
	if r := runSearch(t, s, "NEEDLE", 21000, Backward, false); !r.Found || r.ID != 3 || r.Wrapped {
		t.Fatalf("second backward: %+v", r)
	}
	if r := runSearch(t, s, "NEEDLE", 3, Backward, false); !r.Found || r.ID != 21000 || !r.Wrapped {
		t.Fatalf("backward wrap: %+v", r)
	}
}
