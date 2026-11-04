package input

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
)

func TestFileFilter(t *testing.T) {
	def, _ := NewFileFilter("")
	for name, want := range map[string]bool{
		"app.log": true, "server.txt": true, "noext": true, "cache.2026-09-20.logs.txt": true,
		".hidden.log": false, "app.log.gz": false, "old.zip": false, "app.log.1": false, "app.log.12": false,
		"app.1.log": true,
	} {
		if def.Match(name) != want {
			t.Errorf("default filter %q = %v, want %v", name, !want, want)
		}
	}
	g, err := NewFileFilter("*.LOG, *.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"a.log": true, "B.LOG": true, "x.jsonl": true, "x.txt": false, "a.log.1": false, ".a.log": false, "a.log.gz": false} {
		if g.Match(name) != want {
			t.Errorf("glob filter %q = %v, want %v", name, !want, want)
		}
	}
	explicit, _ := NewFileFilter("app.log*")
	if !explicit.Match("app.log.1") {
		t.Error("an explicit glob must be able to include numbered rotations")
	}
	if _, err := NewFileFilter("[bad"); err == nil {
		t.Error("bad glob must fail")
	}
}

func TestListFilesNewestFirstSkipsDirsAndArchives(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		writeFile(t, p, "x\n")
		os.Chtimes(p, time.Now().Add(-age), time.Now().Add(-age))
	}
	mk("old.log", 3*time.Hour)
	mk("new.log", time.Minute)
	mk("mid.log", time.Hour)
	mk("skip.gz", 0)
	mk(".hid.log", 0)
	os.Mkdir(filepath.Join(dir, "sub.log"), 0o755)
	f, _ := NewFileFilter("")
	got, err := ListFiles(dir, f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "new.log,mid.log,old.log" {
		t.Fatalf("got %v", names)
	}
}

// ---- follower --------------------------------------------------------------

type dirHarness struct {
	ring *engine.RingStore
	sink *testSink
	df   *DirFollower
	stop func()
	dir  string
}

func startDir(t *testing.T, dir string, mod func(*DirOptions)) *dirHarness {
	t.Helper()
	h := &dirHarness{ring: engine.NewRingStore(10000, 0, &engine.Stats{}), sink: &testSink{}, dir: dir}
	o := DirOptions{Dir: dir, Rescan: 40 * time.Millisecond, SilentSwitch: 150 * time.Millisecond,
		Tail: TailOptions{Registry: registry(t), Store: h.ring, Sink: h.sink, Backlog: -1,
			IdleFlush: 40 * time.Millisecond, Coalesce: time.Millisecond, SafetyInterval: 80 * time.Millisecond,
			PollInterval: 20 * time.Millisecond}}
	if mod != nil {
		mod(&o)
	}
	df, err := NewDirFollower(o)
	if err != nil {
		t.Fatal(err)
	}
	h.df = df
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { df.Run(ctx); close(done) }()
	h.stop = func() { cancel(); <-done }
	t.Cleanup(h.stop)
	return h
}

func (h *dirHarness) texts() (out []string) {
	h.ring.Range(0, 0, func(r *engine.LogRecord) bool { out = append(out, r.Message); return true })
	return
}

func (h *dirHarness) sources() map[string]int {
	m := map[string]int{}
	h.ring.Range(0, 0, func(r *engine.LogRecord) bool { m[r.SourceID]++; return true })
	return m
}

func setAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	tm := time.Now().Add(-age)
	if err := os.Chtimes(path, tm, tm); err != nil {
		t.Fatal(err)
	}
}

func TestDirLatestFollowsNewestAndRollsOver(t *testing.T) {
	dir := t.TempDir()
	old, cur := filepath.Join(dir, "a-old.log"), filepath.Join(dir, "b-cur.log")
	writeFile(t, old, "2026-09-19 10:00:00 INFO from-old\n")
	setAge(t, old, time.Hour)
	writeFile(t, cur, "2026-09-20 10:00:00 INFO cur-1\n")
	h := startDir(t, dir, nil)
	waitFor(t, "cur-1", func() bool { return h.ring.Len() == 1 })
	if got := h.df.Files(); len(got) != 1 || got[0] != "b-cur.log" {
		t.Fatalf("must follow only the newest file, got %v", got)
	}

	// Last lines of the old file, then the application rolls over to a new file.
	appendTo(t, cur, "2026-09-20 23:59:59 INFO cur-last\n")
	next := filepath.Join(dir, "c-next.log")
	writeFile(t, next, "2026-09-21 00:00:00 INFO next-1\n2026-09-21 00:00:01 INFO next-2\n")
	waitFor(t, "rollover", func() bool { return h.ring.Len() == 4 })
	if got := strings.Join(h.texts(), ","); got != "cur-1,cur-last,next-1,next-2" {
		t.Fatalf("order must be preserved across the switch (old file drained first): %s", got)
	}
	if got := h.df.Files(); len(got) != 1 || got[0] != "c-next.log" {
		t.Fatalf("files after rollover: %v", got)
	}
	if !h.sink.has(StateRotated) {
		t.Fatal("rollover must be reported")
	}
	appendTo(t, cur, "2026-09-21 00:00:02 INFO stale-write\n") // old file is no longer followed
	appendTo(t, next, "2026-09-21 00:00:03 INFO next-3\n")
	waitFor(t, "next-3", func() bool { return h.ring.Len() == 5 })
	time.Sleep(150 * time.Millisecond)
	for _, s := range h.texts() {
		if s == "stale-write" {
			t.Fatal("old file must not be followed after rollover")
		}
	}
}

func TestDirLatestSwitchesWhenCurrentGoesSilent(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	writeFile(t, a, "2026-09-20 10:00:00 INFO a-1\n")
	setAge(t, a, time.Hour)
	writeFile(t, b, "2026-09-20 10:00:00 INFO b-1\n")
	h := startDir(t, dir, nil)
	waitFor(t, "b-1", func() bool { return h.ring.Len() == 1 })
	// Freshly touched older file: b is silent longer than SilentSwitch, a becomes the newest.
	time.Sleep(250 * time.Millisecond)
	appendTo(t, a, "2026-09-20 10:05:00 INFO a-2\n")
	waitFor(t, "switch to a", func() bool { f := h.df.Files(); return len(f) == 1 && f[0] == "a.log" })
}

func TestDirAllFollowsEverythingAndPicksUpNewFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	writeFile(t, a, "2026-09-20 10:00:00 INFO a-1\n")
	writeFile(t, b, "2026-09-20 10:00:00 INFO b-1\n")
	h := startDir(t, dir, func(o *DirOptions) { o.Mode = DirAll })
	waitFor(t, "both", func() bool { return h.ring.Len() == 2 })
	appendTo(t, a, "2026-09-20 10:00:01 INFO a-2\n")
	appendTo(t, b, "2026-09-20 10:00:01 INFO b-2\n")
	waitFor(t, "appends", func() bool { return h.ring.Len() == 4 })
	c := filepath.Join(dir, "c.log")
	writeFile(t, c, "2026-09-20 10:00:02 INFO c-1\n2026-09-20 10:00:03 INFO c-2\n")
	waitFor(t, "new file read from its first byte", func() bool { return h.ring.Len() == 6 })
	src := h.sources()
	if src["a.log"] != 2 || src["b.log"] != 2 || src["c.log"] != 2 {
		t.Fatalf("records must be tagged with their file: %v", src)
	}
	if got := strings.Join(h.df.Files(), ","); got != "a.log,b.log,c.log" {
		t.Fatalf("files: %s", got)
	}
	os.Remove(a) // deleted files stop being followed
	waitFor(t, "a.log dropped", func() bool { return strings.Join(h.df.Files(), ",") == "b.log,c.log" })
}

// A rotated file is renamed, not new: re-reading it would duplicate every record.
func TestDirRotationRenameDoesNotDuplicate(t *testing.T) {
	for _, mode := range []DirMode{DirLatest, DirAll} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "app.log")
			writeFile(t, p, "2026-09-20 10:00:00 INFO one\n2026-09-20 10:00:01 INFO two\n")
			h := startDir(t, dir, func(o *DirOptions) { o.Mode = mode })
			waitFor(t, "initial", func() bool { return h.ring.Len() == 2 })
			appendTo(t, p, "2026-09-20 10:00:02 INFO three\n")
			os.Rename(p, filepath.Join(dir, "app.log.old")) // matches the default filter
			writeFile(t, p, "2026-09-20 10:01:00 INFO four\n")
			waitFor(t, "four", func() bool { return h.ring.Len() >= 4 })
			time.Sleep(300 * time.Millisecond) // give a wrong implementation time to re-read app.log.old
			if got := strings.Join(h.texts(), ","); got != "one,two,three,four" {
				t.Fatalf("duplicated or lost records: %s", got)
			}
		})
	}
}

func TestDirAllRespectsMaxFilesKeepingNewest(t *testing.T) {
	dir := t.TempDir()
	for i, n := range []string{"a", "b", "c", "d", "e"} {
		p := filepath.Join(dir, n+".log")
		writeFile(t, p, "2026-09-20 10:00:00 INFO "+n+"\n")
		setAge(t, p, time.Duration(5-i)*time.Hour) // e is newest
	}
	h := startDir(t, dir, func(o *DirOptions) { o.Mode = DirAll; o.MaxFiles = 2 })
	waitFor(t, "two files", func() bool { return h.ring.Len() == 2 })
	time.Sleep(150 * time.Millisecond)
	if got := strings.Join(h.df.Files(), ","); got != "d.log,e.log" {
		t.Fatalf("must keep the newest two files, got %s", got)
	}
}

func TestDirWaitsForFilesAndIgnoresExcluded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".hidden.log"), "2026-09-20 10:00:00 INFO hidden\n")
	writeFile(t, filepath.Join(dir, "old.log.gz"), "binary")
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	h := startDir(t, dir, nil)
	waitFor(t, "waiting state", func() bool { return h.sink.has(StateWaiting) })
	if h.ring.Len() != 0 || len(h.df.Files()) != 0 {
		t.Fatalf("nothing should be followed: %v", h.df.Files())
	}
	writeFile(t, filepath.Join(dir, "real.log"), "2026-09-20 10:00:00 INFO real\n")
	waitFor(t, "real", func() bool { return h.ring.Len() == 1 })
}

func TestDirPollingFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.log"), "2026-09-20 10:00:00 INFO a\n")
	h := startDir(t, dir, func(o *DirOptions) { o.Tail.ForcePoll = true })
	waitFor(t, "a", func() bool { return h.ring.Len() == 1 })
	appendTo(t, filepath.Join(dir, "a.log"), "2026-09-20 10:00:01 INFO b\n")
	waitFor(t, "b via polling", func() bool { return h.ring.Len() == 2 })
	writeFile(t, filepath.Join(dir, "z.log"), "2026-09-20 10:00:02 INFO z\n")
	waitFor(t, "rollover via polling", func() bool { return h.ring.Len() == 3 })
}

func TestDirFollowerLeaksNoGoroutines(t *testing.T) {
	base := runtime.NumGoroutine()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.log"), "2026-09-20 10:00:00 INFO a\n")
	writeFile(t, filepath.Join(dir, "b.log"), "2026-09-20 10:00:00 INFO b\n")
	for i := 0; i < 4; i++ {
		h := startDir(t, dir, func(o *DirOptions) { o.Mode = DirAll })
		waitFor(t, "records", func() bool { return h.ring.Len() == 2 })
		h.stop()
	}
	time.Sleep(150 * time.Millisecond)
	if n := runtime.NumGoroutine(); n > base+3 {
		t.Fatalf("goroutines grew from %d to %d", base, n)
	}
}

func TestNewDirFollowerRejectsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.log")
	writeFile(t, p, "x\n")
	_, err := NewDirFollower(DirOptions{Dir: p, Tail: TailOptions{Registry: registry(t), Store: engine.NewRingStore(1, 0, nil), Sink: &testSink{}}})
	if err == nil {
		t.Fatal("a regular file is not a directory")
	}
}
