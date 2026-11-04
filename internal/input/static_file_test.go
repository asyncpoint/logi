package input

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/platform"
	"github.com/asyncpoint/logi/internal/plugins"
)

func staticFile(t *testing.T, lines int) (string, string) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		b.WriteString("2026-09-19 10:00:00 INFO message ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	p := filepath.Join(t.TempDir(), "s.log")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, b.String()
}

// With a handle supplied, OpenStatic must not open the path again: the file is
// removed first, so a second open by name would fail.
func TestOpenStaticUsesTheSuppliedHandle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an open file cannot be removed on Windows")
	}
	reg, _ := plugins.BuildRegistry(nil)
	p, content := staticFile(t, 300)
	f, err := platform.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	sample := []byte(content)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStatic(StaticOptions{Path: p, File: f, Profile: reg.Detect(sample), Sample: sample, BlockSize: 64})
	if err != nil {
		t.Fatalf("OpenStatic reopened the path instead of using the handle: %v", err)
	}
	if err := st.Index(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.Len() != 300 {
		t.Fatalf("indexed %d records, want 300", st.Len())
	}
	if rec, ok := st.Get(150); !ok || !strings.Contains(rec.Raw.Data+rec.Message, "message") {
		t.Fatalf("Get(150) = %+v, %v", rec, ok)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Stat(); err == nil {
		t.Fatal("Close must close the handle the store took over")
	}
}

// On failure the caller keeps ownership of a supplied handle; OpenStatic must not
// close it behind the caller's back.
func TestOpenStaticLeavesASuppliedHandleToTheCallerOnError(t *testing.T) {
	reg, _ := plugins.BuildRegistry(nil)
	p, content := staticFile(t, 5)
	f, err := platform.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Close() // an unusable handle makes the identity lookup fail
	if _, err := OpenStatic(StaticOptions{Path: p, File: f, Profile: reg.Detect([]byte(content)), Sample: []byte(content)}); err == nil {
		t.Fatal("expected an error for a closed handle")
	}
}

func TestOpenStaticStillOpensByPathWithoutAHandle(t *testing.T) {
	reg, _ := plugins.BuildRegistry(nil)
	p, content := staticFile(t, 40)
	st, err := OpenStatic(StaticOptions{Path: p, Profile: reg.Detect([]byte(content)), Sample: []byte(content)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Index(context.Background()); err != nil || st.Len() != 40 {
		t.Fatalf("index: err=%v len=%d", err, st.Len())
	}
}

// A backward scan window covers about one read segment per scan worker.
func TestScanWindowScalesWithWorkers(t *testing.T) {
	reg, _ := plugins.BuildRegistry(nil)
	p, content := staticFile(t, 4000)
	window := func(workers int) uint64 {
		st, err := OpenStatic(StaticOptions{Path: p, Profile: reg.Detect([]byte(content)), Sample: []byte(content), ScanWorkers: workers, BlockSize: 64})
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.Index(context.Background()); err != nil {
			t.Fatal(err)
		}
		return st.ScanWindow(1)
	}
	w1, w4 := window(1), window(4)
	if w1 == 0 || w4 < 3*w1 || w4 > 5*w1 {
		t.Fatalf("window with 1 worker = %d, with 4 = %d; want about 4x", w1, w4)
	}
	if got := func() uint64 {
		st, _ := OpenStatic(StaticOptions{Path: p, Profile: reg.Detect([]byte(content)), Sample: []byte(content)})
		defer st.Close()
		return st.ScanWindow(4096) // nothing indexed yet: fall back to the minimum
	}(); got != 4096 {
		t.Fatalf("window before indexing = %d, want the 4096 minimum", got)
	}
}
