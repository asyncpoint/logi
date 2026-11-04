package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/config"
)

// An old log: one record a minute for an hour, long before "now".
func hourOfLogs() string {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "2020-01-01 10:%02d:00 INFO tick %d\n", i, i)
	}
	return b.String()
}

func viewRowsAfterFilter(t *testing.T, s *Session, want int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d rows in the view", want), func() bool { st := s.Status(); return !st.Filtering && st.IndexDone && st.ViewRows == want })
}

// "since 15m" means the last 15 minutes *of the log*, not of the machine's clock.
func TestSinceIsMeasuredFromTheNewestRecord(t *testing.T) {
	for _, c := range []struct {
		filter string
		want   int
	}{
		{`since:"15m"`, 16},                   // 10:44 .. 10:59
		{`since:"15m" until:"10m"`, 6},        // 10:44 .. 10:49
		{`since:"2020-01-01 10:30"`, 30},      // an absolute reading of the log's own clock
		{`until:"2020-01-01"`, 60},            // a bare until date includes the whole day
		{`since:"2020-01-01 10:58" -tick`, 0}, // combines with other terms
		{`since:"1h"`, 60},                    // the whole log is within an hour of its end
	} {
		s := newSession(t, hourOfLogs(), func(cfg *config.Config) { cfg.Filter = c.filter })
		viewRowsAfterFilter(t, s, c.want)
	}
}

func TestSinceCanBeSetFromTheFilterBarToo(t *testing.T) {
	s := newSession(t, hourOfLogs(), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	if err := s.SetFilter(`since:"5m"`); err != nil {
		t.Fatal(err)
	}
	viewRowsAfterFilter(t, s, 6)
	if err := s.SetFilter(`since:"soon"`); err == nil {
		t.Fatal("an unparseable time must be reported, not ignored")
	}
}

func linesFile(t *testing.T, dir, name string, n int) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "2026-10-15 10:%02d:%02d INFO %s line %d\n", i/60, i%60, name, i)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func liveSession(t *testing.T, cfg config.Config) *Session {
	t.Helper()
	cfg.Follow, cfg.ForcePoll, cfg.BatchInterval = true, true, 2*time.Millisecond
	cfg.IdleFlush = 40 * time.Millisecond
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	return s
}

func TestLinesStartsTheLiveViewWithTheLastNLines(t *testing.T) {
	p := linesFile(t, t.TempDir(), "app.log", 100)
	cfg := config.Defaults()
	cfg.Path, cfg.Lines, cfg.LinesSet = p, 10, true
	s := liveSession(t, cfg)
	waitFor(t, "10 records", func() bool { return s.Status().Records == 10 })
	if r, ok := s.ViewAt(0); !ok || !strings.Contains(r.Message, "line 90") {
		t.Fatalf("the view should start at line 90, got %+v", r)
	}
	time.Sleep(150 * time.Millisecond)
	if n := s.Status().Records; n != 10 {
		t.Fatalf("more than the last 10 lines were loaded: %d", n)
	}
}

func TestLinesAppliesToEachFileOfADirectory(t *testing.T) {
	dir := t.TempDir()
	linesFile(t, dir, "a.log", 50)
	linesFile(t, dir, "b.log", 50)
	cfg := config.Defaults()
	cfg.Path, cfg.DirMode, cfg.Lines, cfg.LinesSet = dir, "all", 5, true
	s := liveSession(t, cfg)
	waitFor(t, "5 lines from each of the two files", func() bool { return s.Status().Records == 10 })
}

func TestStandardInputSession(t *testing.T) {
	pr, pw := io.Pipe()
	cfg := config.Defaults()
	cfg.BatchInterval, cfg.IdleFlush = 2*time.Millisecond, 40*time.Millisecond
	cfg.Filter = "severity:>=warn"
	s, err := NewSessionFromReader(cfg, pr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); pw.Close(); s.Close() })
	if s.Mode() != ModeLive || s.Status().Path != "stdin" {
		t.Fatalf("stdin is a live source named stdin: mode=%v path=%q", s.Mode(), s.Status().Path)
	}
	for i := 0; i < 20; i++ {
		lvl := "INFO"
		if i%5 == 0 {
			lvl = "ERROR"
		}
		fmt.Fprintf(pw, "2026-10-15 10:00:%02d %s piped line %d\n", i, lvl, i)
	}
	waitFor(t, "20 records ingested and the 4 errors shown", func() bool {
		st := s.Status()
		return st.Records == 19 || st.Records == 20 // the last line may await its idle flush
	})
	waitFor(t, "filter applied to a stream", func() bool { st := s.Status(); return !st.Filtering && st.ViewRows == 4 })
	pw.Close()
	waitFor(t, "end of input is reported", func() bool { return s.Status().Source.String() == "ended" })
}

func TestMissingFileErrorIsFriendly(t *testing.T) {
	cfg := config.Defaults()
	cfg.Path = filepath.Join(t.TempDir(), "missing.log")
	_, err := NewSession(cfg)
	if err == nil || !strings.Contains(err.Error(), "cannot find") || strings.Contains(err.Error(), "stat ") {
		t.Fatalf("want a plain 'cannot find' message, got %v", err)
	}
}

// With input piped in, the user can still open a file from the picker (Ctrl+O).
// That reuses the configuration, which still says "stdin", but now names a file:
// the file must be opened, and the keyboard must never be read as a log.
func TestStdinConfigWithAFileNamesTheFile(t *testing.T) {
	p := linesFile(t, t.TempDir(), "other.log", 5)
	cfg := config.Defaults()
	cfg.Stdin, cfg.Path = true, p
	cfg.BatchInterval = 2 * time.Millisecond
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	if s.Mode() != ModeStatic || s.Status().Path != p {
		t.Fatalf("a chosen file must be opened as a file: mode=%v path=%q", s.Mode(), s.Status().Path)
	}
	waitFor(t, "5 records", func() bool { return s.Status().Records == 5 })
}

// docker compose logs | logi: each service becomes a source with its own colour
// slot, stack traces stay with their service, and the view is multi-source.
func TestComposeStreamIsAMultiSourceSession(t *testing.T) {
	pr, pw := io.Pipe()
	cfg := config.Defaults()
	cfg.BatchInterval, cfg.IdleFlush = 2*time.Millisecond, 40*time.Millisecond
	s, err := NewSessionFromReader(cfg, pr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); pw.Close(); s.Close() })
	if s.MultiSource() {
		t.Fatal("nothing is known about the stream yet")
	}
	for i := 0; i < 6; i++ {
		fmt.Fprintf(pw, "web-1  | 2026-10-15 10:00:%02d INFO request %d\n", i*2, i)
		fmt.Fprintf(pw, "db-1   | 2026-10-15 10:00:%02d WARN slow query %d\n", i*2+1, i)
	}
	waitFor(t, "12 records from two services", func() bool { return s.Status().Records >= 11 })
	if !s.MultiSource() || s.FollowedFileCount() != 2 {
		t.Fatalf("multi=%v sources=%d, want a two-source view", s.MultiSource(), s.FollowedFileCount())
	}
	if a, b := s.SourceSlot("web-1"), s.SourceSlot("db-1"); a == b {
		t.Fatalf("each service needs its own colour slot, both got %d", a)
	}
	// filtering by service works through the usual source: term
	if err := s.SetFilter("source:db-1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "only db-1", func() bool { st := s.Status(); return !st.Filtering && st.ViewRows >= 5 })
	for i := 0; i < s.Status().ViewRows; i++ {
		if r, ok := s.ViewAt(i); ok && r.SourceID != "db-1" {
			t.Fatalf("row %d is from %q", i, r.SourceID)
		}
	}
}

func TestPlainStreamIsNotMultiSource(t *testing.T) {
	pr, pw := io.Pipe()
	cfg := config.Defaults()
	cfg.BatchInterval, cfg.IdleFlush = 2*time.Millisecond, 40*time.Millisecond
	s, _ := NewSessionFromReader(cfg, pr)
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); pw.Close(); s.Close() })
	for i := 0; i < 5; i++ {
		fmt.Fprintf(pw, "2026-10-15 10:00:%02d INFO plain %d\n", i, i)
	}
	waitFor(t, "records", func() bool { return s.Status().Records >= 4 })
	if s.MultiSource() || s.FollowedFileCount() != 0 {
		t.Fatal("an ordinary stream has no sources to colour")
	}
}
