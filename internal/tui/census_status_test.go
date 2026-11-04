package tui

import (
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
)

func statusLineOf(r *rig) string {
	lines := strings.Split(r.raw(), "\n")
	return engine.StripANSI(lines[len(lines)-1])
}

// Static mode shows the same E:/W: counters as live mode, and the file size after them.
func TestStaticStatusBarShowsSeverityCounts(t *testing.T) {
	r := newRig(t, logText(500), nil)
	r.indexed()
	r.settle("severity counts", func() bool { st := r.s.Status(); return st.Counts && !st.Counting })
	status := statusLineOf(r)
	for _, want := range []string{"E:50", "W:50"} {
		if !strings.Contains(status, want) {
			t.Fatalf("static status bar lacks %q: %q", want, status)
		}
	}
	if strings.Contains(status, "counting") {
		t.Errorf("the counting marker should be gone once the scan is done: %q", status)
	}
	if e, w, size := strings.Index(status, "E:50"), strings.Index(status, "W:50"), strings.Index(status, "KiB"); !(e < w && w < size) {
		t.Errorf("order should be errors, warnings, then file size: %q", status)
	}
}

func TestStaticStatusBarWithoutCountsWhenSwitchedOff(t *testing.T) {
	r := newRig(t, logText(500), func(c *config.Config) { c.StaticCounts = false })
	r.indexed()
	if status := statusLineOf(r); strings.Contains(status, "E:") || strings.Contains(status, "W:") {
		t.Fatalf("counters shown although switched off: %q", status)
	}
}
