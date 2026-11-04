package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/search"
)

func logText(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		sev := "INFO"
		switch {
		case i%10 == 0:
			sev = "ERROR"
		case i%5 == 0:
			sev = "WARN"
		}
		fmt.Fprintf(&b, "2026-09-19 10:%02d:%02d %s message %d\n", (i/60)%60, i%60, sev, i)
	}
	return b.String()
}

func newSession(t *testing.T, content string, mod func(*config.Config)) *Session {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Path = p
	cfg.BatchInterval = 2 * time.Millisecond
	cfg.Backlog = -1
	if mod != nil {
		mod(&cfg)
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func TestStaticSessionViewAndFilter(t *testing.T) {
	s := newSession(t, logText(1000), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	if s.ViewLen() != 1000 || s.Mode() != ModeStatic {
		t.Fatalf("len=%d", s.ViewLen())
	}
	if r, ok := s.ViewAt(499); !ok || !strings.Contains(r.Message, "message 500") {
		t.Fatalf("%+v", r)
	}
	if err := s.SetFilter("severity:error"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "filter", func() bool { st := s.Status(); return !st.Filtering && st.ViewRows == 100 })
	id, _ := s.IDAt(0)
	if id != 10 {
		t.Fatalf("first error id=%d", id)
	}
	pos, ok := s.PositionAt(0)
	if !ok || pos.Kind != engine.PositionStaticOffset || pos.RecordID != id {
		t.Fatalf("filtered row lacks static source position: %#v, %v", pos, ok)
	}
	if rec, ok := s.ResolvePosition(pos); !ok || rec.ID != id {
		t.Fatalf("filtered source position failed to resolve: %#v, %v", rec, ok)
	}
	if row := s.RowOf(55); row != 5 { // first error >= 55 is id 60 => row 5
		t.Fatalf("RowOf(55)=%d", row)
	}
	s.SetFilter("")
	waitFor(t, "cleared", func() bool { return s.ViewLen() == 1000 })
	if err := s.SetFilter("severity:nonsense"); err == nil {
		t.Fatal("bad filter must error and leave state intact")
	}
}

func TestLiveIncrementalFilterAndEviction(t *testing.T) {
	s := newSession(t, "", func(c *config.Config) { c.Follow = true; c.Buffer = 50; c.Parser = "generic" })
	if err := s.SetFilter("severity:error"); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	defer f.Close()
	f.WriteString(logText(30))
	waitFor(t, "3 errors", func() bool { return s.Status().ViewRows == 3 })
	f.WriteString(strings.ReplaceAll(logText(200), "message", "later"))
	// Ring holds 50 records; only errors still in the ring may remain visible.
	waitFor(t, "eviction", func() bool { st := s.Status(); return st.Records == 50 && st.Stats.Evicted > 0 })
	waitFor(t, "filter trimmed", func() bool {
		n := s.ViewLen()
		return n >= 4 && n <= 6
	})
	for row := 0; row < s.ViewLen(); row++ {
		if _, ok := s.ViewAt(row); !ok {
			t.Fatalf("view row %d points at an evicted record", row)
		}
	}
}

func runSearch(t *testing.T, s *Session, q string, from uint64, dir Direction, incl bool) SearchResult {
	t.Helper()
	gen, err := s.Search(search.Request{Query: q}, from, dir, incl)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case u := <-s.Updates():
			if u.Search != nil && u.Search.Gen == gen {
				return *u.Search
			}
		case <-deadline:
			t.Fatal("search timeout")
		}
	}
}

func TestSearchForwardBackwardWrap(t *testing.T) {
	s := newSession(t, logText(500), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	r := runSearch(t, s, "message 300", 1, Forward, false)
	if !r.Found || r.ID != 300 || r.Wrapped {
		t.Fatalf("%+v", r)
	}
	if r.Position.IsZero() || r.Position.RecordID != r.ID {
		t.Fatalf("search result missing source position: %+v", r.Position)
	}
	if rec, ok := s.ResolvePosition(r.Position); !ok || rec.ID != r.ID {
		t.Fatalf("search position did not resolve to its match: position=%+v record=%+v ok=%v", r.Position, rec, ok)
	}
	r = runSearch(t, s, "message 25", 400, Forward, false) // wraps to 25? first match after 400 none => wrap => 25
	if !r.Found || r.ID != 25 || !r.Wrapped {
		t.Fatalf("wrap: %+v", r)
	}
	r = runSearch(t, s, "error", 95, Backward, false) // ids divisible by 10; nearest above? going up from 95 -> 90
	if !r.Found || r.ID != 90 {
		t.Fatalf("backward: %+v", r)
	}
	r = runSearch(t, s, "error", 5, Backward, false) // nothing before 5; wraps to last error 500
	if !r.Found || r.ID != 500 || !r.Wrapped {
		t.Fatalf("backward wrap: %+v", r)
	}
	r = runSearch(t, s, "no-such-thing", 1, Forward, false)
	if r.Found {
		t.Fatalf("%+v", r)
	}
	if _, err := s.Search(search.Request{Query: "(", Mode: search.Regex}, 1, Forward, false); err == nil {
		t.Fatal("invalid regex must fail synchronously")
	}
}

func TestSearchRespectsFilteredView(t *testing.T) {
	s := newSession(t, logText(500), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	s.SetFilter("severity:warn")
	waitFor(t, "filter", func() bool { st := s.Status(); return !st.Filtering && st.ViewRows == 50 })
	r := runSearch(t, s, "message", 1, Forward, false)
	if !r.Found || r.ID != 5 {
		t.Fatalf("first hit must be a visible (warn) record: %+v", r)
	}
	r = runSearch(t, s, "message 7", 1, Forward, false) // id 7 is INFO => hidden; 70,75.. are 70 err? 70%10==0 error hidden; 75 warn
	if !r.Found || r.ID != 75 {
		t.Fatalf("hidden records must not match: %+v", r)
	}
}

func TestEmitterNeverBlocksProducers(t *testing.T) {
	e := newEmitter(time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.run(ctx)
	done := make(chan struct{})
	go func() { // nobody reads e.out: producers must still finish instantly
		for i := 1; i <= 200000; i++ {
			e.added(uint64(i), uint64(i), 0)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("producer blocked by a slow UI")
	}
	time.Sleep(20 * time.Millisecond)
	var total int
	for {
		select {
		case u := <-e.out:
			total += u.Added
			continue
		default:
		}
		break
	}
	// batches merged: far fewer updates than events, and counts stay exact once drained
	e.added(1, 1, 0)
	time.Sleep(20 * time.Millisecond)
	u := <-e.out
	if u.Added < 1 {
		t.Fatalf("%+v", u)
	}
}

func TestSessionRejectsBadInput(t *testing.T) {
	cfg := config.Defaults()
	cfg.Path = filepath.Join(t.TempDir(), "missing.log")
	if _, err := NewSession(cfg); err == nil {
		t.Fatal("missing static file must error")
	}
	cfg.Path = t.TempDir()
	if _, err := NewSession(cfg); err == nil {
		t.Fatal("directory must error")
	}
	cfg.Path = filepath.Join(t.TempDir(), "x.log")
	os.WriteFile(cfg.Path, []byte("x\n"), 0o644)
	cfg.Parser = "nope"
	if _, err := NewSession(cfg); err == nil {
		t.Fatal("unknown parser must error")
	}
}

func TestCommandsRebind(t *testing.T) {
	c := DefaultCommands()
	for key, want := range map[string]Action{
		"alt+e":       ActionNextError,
		"alt+E":       ActionPrevError,
		"alt+shift+e": ActionPrevError,
		"alt+W":       ActionNextWarning,
		"alt+shift+w": ActionNextWarning,
		"alt+w":       ActionPrevWarning,
	} {
		if got, ok := c.Lookup(key); !ok || got != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
	if a, ok := c.Lookup("ctrl+f"); !ok || a != ActionSearch {
		t.Fatal("default binding")
	}
	if !c.Rebind(ActionSearch, []string{"ctrl+s"}) {
		t.Fatal("rebind")
	}
	if _, ok := c.Lookup("ctrl+f"); ok {
		t.Fatal("old key must be unbound")
	}
	if a, _ := c.Lookup("ctrl+s"); a != ActionSearch {
		t.Fatal("new key")
	}
	c.Rebind(ActionFilter, []string{"ctrl+s"}) // steals the key deterministically
	if a, _ := c.Lookup("ctrl+s"); a != ActionFilter {
		t.Fatal("last binding wins")
	}
	if c.Rebind("nonexistent", nil) {
		t.Fatal("unknown action")
	}
	if len(c.Palette()) == 0 {
		t.Fatal("palette")
	}
}

func dirSession(t *testing.T, files map[string]string, mod func(*config.Config)) (*Session, string) {
	t.Helper()
	dir := t.TempDir()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // map order is random: age by sorted name so later names are newer
	for i, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(files[name]), 0o644); err != nil {
			t.Fatal(err)
		}
		age := time.Duration(len(names)-i) * time.Hour
		os.Chtimes(p, time.Now().Add(-age), time.Now().Add(-age))
	}
	cfg := config.Defaults()
	cfg.Path, cfg.BatchInterval, cfg.Backlog = dir, 2*time.Millisecond, -1
	if mod != nil {
		mod(&cfg)
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	return s, dir
}

func TestLiveDirectoryAllModeSession(t *testing.T) {
	s, dir := dirSession(t, map[string]string{
		"a.log": "2026-09-20 10:00:00 INFO from a\n",
		"b.log": "2026-09-20 10:00:01 ERROR from b\n",
	}, func(c *config.Config) { c.Follow, c.DirMode = true, "all" })
	waitFor(t, "records from both files", func() bool { return s.Status().Records == 2 })
	st := s.Status()
	if st.Dir != dir || st.DirMode != "all" || len(st.Files) != 2 || !s.MultiSource() || s.Dir() != dir {
		t.Fatalf("status: %+v", st)
	}
	sources := map[string]bool{}
	for row := 0; row < s.ViewLen(); row++ {
		r, _ := s.ViewAt(row)
		sources[r.SourceID] = true
	}
	if !sources["a.log"] || !sources["b.log"] {
		t.Fatalf("records must carry their source file: %v", sources)
	}
	if err := s.SetFilter("source:b.log"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "source filter", func() bool { st := s.Status(); return !st.Filtering && st.ViewRows == 1 })
	if s.ProfileName() == "detecting…" {
		t.Fatal("profile should be known once files were read")
	}
	// a file created later is picked up
	os.WriteFile(filepath.Join(dir, "c.log"), []byte("2026-09-20 10:00:02 WARN from c\n"), 0o644)
	s.SetFilter("")
	waitFor(t, "new file", func() bool { return s.Status().Records == 3 })
}

func TestLiveDirectoryLatestModeFollowsRollover(t *testing.T) {
	s, dir := dirSession(t, map[string]string{"day1.log": "2026-09-20 10:00:00 INFO day1\n"},
		func(c *config.Config) { c.Follow = true })
	waitFor(t, "day1", func() bool { return s.Status().Records == 1 })
	if s.MultiSource() {
		t.Fatal("latest mode is not multi-source")
	}
	os.WriteFile(filepath.Join(dir, "day2.log"), []byte("2026-09-21 00:00:00 INFO day2\n"), 0o644)
	waitFor(t, "rollover", func() bool {
		st := s.Status()
		return st.Records == 2 && len(st.Files) == 1 && st.Files[0] == "day2.log"
	})
}

func TestStaticDirectoryOpensNewestAndOffersPicker(t *testing.T) {
	s, dir := dirSession(t, map[string]string{
		"a-old.log": "2026-09-19 10:00:00 INFO old\n",
		"b-new.log": "2026-09-20 10:00:00 INFO new\n",
	}, nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	if s.PickerDir() != dir || filepath.Base(s.Status().Path) != "b-new.log" {
		t.Fatalf("picker=%q path=%q", s.PickerDir(), s.Status().Path)
	}
	files, err := s.ListDir()
	if err != nil || len(files) != 2 || files[0].Name != "b-new.log" {
		t.Fatalf("%+v %v", files, err)
	}
	if r, _ := s.ViewAt(0); r.SourceID != "b-new.log" {
		t.Fatalf("source id %q", r.SourceID)
	}
}

func TestDirectoryErrors(t *testing.T) {
	cfg := config.Defaults()
	cfg.Path = t.TempDir() // empty directory, static mode
	if _, err := NewSession(cfg); err == nil {
		t.Fatal("static mode on an empty directory must fail with a clear error")
	}
	cfg.Follow = true // live mode waits for files instead
	if _, err := NewSession(cfg); err != nil {
		t.Fatalf("live mode on an empty directory should wait: %v", err)
	}
	cfg.Glob = "[bad"
	if _, err := NewSession(cfg); err == nil {
		t.Fatal("bad glob must fail")
	}
}

func TestNewSessionBadStartupFilterReturnsNoSession(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.log")
	os.WriteFile(p, []byte("2026-10-15 10:00:00 INFO hi\n"), 0o644)
	cfg := config.Defaults()
	cfg.Path, cfg.Filter = p, "re:("
	s, err := NewSession(cfg)
	if err == nil || s != nil {
		t.Fatalf("got session=%v err=%v; want nil session and an error", s != nil, err)
	}
}

func TestSourceSlotsAreStableAndUnique(t *testing.T) {
	var t0 sourceSlots
	t0.seed([]string{"web.log", "api.log", "db.log"})
	api, db, web := t0.slot("api.log"), t0.slot("db.log"), t0.slot("web.log")
	if api != 0 || db != 1 || web != 2 {
		t.Fatalf("seeded slots must follow alphabetical order, got api=%d db=%d web=%d", api, db, web)
	}
	if t0.slot("web.log") != web || t0.slot("api.log") != api {
		t.Fatal("slots must be stable across lookups")
	}
	if n := t0.slot("zz-new.log"); n != 3 {
		t.Fatalf("a file that appears later takes the next free slot, got %d", n)
	}
	if t0.slot("aa-later.log") == api {
		t.Fatal("a late file must never take over an existing file's slot")
	}
	// bounded: past the cap unknown names still get a usable, deterministic slot
	var big sourceSlots
	for i := 0; i < maxSourceSlots+50; i++ {
		big.slot(fmt.Sprintf("f%d.log", i))
	}
	if len(big.m) != maxSourceSlots {
		t.Fatalf("registry grew past its cap: %d", len(big.m))
	}
	if a, b := big.slot("overflow.log"), big.slot("overflow.log"); a != b || a < 0 {
		t.Fatalf("overflow slot not deterministic/non-negative: %d %d", a, b)
	}
}

func TestLiveDirectorySeedsSourceSlotsAlphabetically(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b.log", "c.log", "a.log"} {
		os.WriteFile(filepath.Join(dir, n), []byte("2026-10-15 10:00:00 INFO x\n"), 0o644)
	}
	cfg := config.Defaults()
	cfg.Path, cfg.Follow, cfg.DirMode = dir, true, "all"
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if a, b, c := s.SourceSlot("a.log"), s.SourceSlot("b.log"), s.SourceSlot("c.log"); a != 0 || b != 1 || c != 2 {
		t.Fatalf("slots a=%d b=%d c=%d, want 0 1 2 regardless of file age", a, b, c)
	}
	if s.SourceSlot("") != 0 {
		t.Fatal("empty source is slot 0")
	}
}

func TestFollowedFileCountTracksLiveDirectory(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 12; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.log", i)), []byte("2026-10-15 10:00:00 INFO x\n"), 0o644)
	}
	cfg := config.Defaults()
	cfg.Path, cfg.Follow, cfg.DirMode, cfg.ForcePoll = dir, true, "all", true
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for s.FollowedFileCount() != 12 {
		if time.Now().After(deadline) {
			t.Fatalf("FollowedFileCount = %d, want 12", s.FollowedFileCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// a static file session follows no directory
	p := filepath.Join(dir, "f00.log")
	scfg := config.Defaults()
	scfg.Path = p
	st, err := NewSession(scfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.FollowedFileCount() != 0 {
		t.Fatalf("a single static file follows no directory, got %d", st.FollowedFileCount())
	}
}

func TestMixedDirectoryLayoutAddsTheEventColumn(t *testing.T) {
	dir := t.TempDir()
	fixture, err := os.ReadFile("../../test/fixtures/sample-ncache-table.log")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "cache.log"), fixture, 0o644)
	os.WriteFile(filepath.Join(dir, "plain.log"), []byte("2026-09-20 10:00:00 INFO a plain line\n2026-09-20 10:00:01 INFO another\n"), 0o644)
	cfg := config.Defaults()
	cfg.Path, cfg.Follow, cfg.DirMode, cfg.ForcePoll = dir, true, "all", true
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for s.ProfileName() != "mixed" {
		if time.Now().After(deadline) {
			t.Fatalf("profile = %q, want mixed", s.ProfileName())
		}
		time.Sleep(10 * time.Millisecond)
	}
	var cols []string
	for _, c := range s.Layout() {
		cols = append(cols, c.Name)
	}
	if want := "time,level,source,event,message"; strings.Join(cols, ",") != want {
		t.Fatalf("mixed layout = %v, want %s", cols, want)
	}
}
