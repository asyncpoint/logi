package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/search"
)

func openCfg(path string) config.Config {
	cfg := config.Defaults()
	cfg.Path = path
	cfg.BatchInterval = 2 * time.Millisecond
	cfg.Backlog = -1
	return cfg
}

func writeLog(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectKind(t *testing.T) {
	dir := t.TempDir()
	file := writeLog(t, dir, "a.log", logText(3))
	cases := []struct {
		name string
		mod  func(*config.Config)
		path string
		want sourceKind
	}{
		{"stdin", func(c *config.Config) { c.Stdin = true }, "", kindStdin},
		{"static file", nil, file, kindStaticFile},
		{"static directory", nil, dir, kindStaticDir},
		{"live file", func(c *config.Config) { c.Follow = true }, file, kindLiveFile},
		{"live directory", func(c *config.Config) { c.Follow = true }, dir, kindLiveDir},
		{"live file that does not exist yet", func(c *config.Config) { c.Follow = true }, filepath.Join(dir, "later.log"), kindLiveFile},
		{"static path that does not exist", nil, filepath.Join(dir, "nope.log"), kindStaticFile},
		{"stdin flag is ignored when a path is given", func(c *config.Config) { c.Stdin = true }, file, kindStaticFile},
	}
	for _, c := range cases {
		cfg := openCfg(c.path)
		cfg.Follow = false
		if c.mod != nil {
			c.mod(&cfg)
		}
		if got := detectKind(cfg); got != c.want {
			t.Errorf("%s: kind = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEveryKindHasAnOpener(t *testing.T) {
	for k := kindStdin; k <= kindStaticFile; k++ {
		if openers[k] == nil {
			t.Errorf("kind %v has no opener", k)
		}
	}
}

func TestStringersAreBoundsSafe(t *testing.T) {
	if Mode(9).String() != "unknown" || Mode(-1).String() != "unknown" || sourceKind(9).String() != "unknown" || search.Mode(9).String() != "unknown" {
		t.Fatal("out-of-range values must not panic")
	}
	if ModeStatic.String() != "static" || ModeLive.String() != "live" || kindLiveDir.String() != "live directory" {
		t.Fatal("known values must keep their names")
	}
}

func TestOpenStaticFileWiresStaticStore(t *testing.T) {
	s := newSession(t, logText(20), nil)
	if s.Mode() != ModeStatic || s.static == nil || s.ring != nil || s.tailer != nil || s.dirf != nil || s.dir != "" {
		t.Fatalf("static file wiring: mode=%v static=%v ring=%v tailer=%v dirf=%v dir=%q", s.Mode(), s.static != nil, s.ring != nil, s.tailer != nil, s.dirf != nil, s.dir)
	}
	if s.profile == nil {
		t.Fatal("static open must pick a profile from the sample")
	}
}

func TestOpenLiveFileWiresRingAndTailer(t *testing.T) {
	s := newSession(t, logText(20), func(c *config.Config) { c.Follow = true })
	if s.Mode() != ModeLive || s.ring == nil || s.tailer == nil || s.static != nil || s.dirf != nil || s.dir != "" {
		t.Fatalf("live file wiring: mode=%v ring=%v tailer=%v static=%v dirf=%v", s.Mode(), s.ring != nil, s.tailer != nil, s.static != nil, s.dirf != nil)
	}
	waitFor(t, "records", func() bool { return s.Status().Records == 20 })
}

func TestOpenLiveDirectoryWiresFollower(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log", logText(5))
	cfg := openCfg(dir)
	cfg.Follow = true
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.dirf == nil || s.ring == nil || s.filter == nil || s.dir != dir || s.tailer != nil || s.static != nil {
		t.Fatalf("live dir wiring: dirf=%v ring=%v filter=%v dir=%q", s.dirf != nil, s.ring != nil, s.filter != nil, s.dir)
	}
}

func TestOpenStaticDirectoryOpensNewestFile(t *testing.T) {
	dir := t.TempDir()
	old := writeLog(t, dir, "old.log", logText(3))
	newer := writeLog(t, dir, "new.log", logText(7))
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(openCfg(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.static == nil || s.Mode() != ModeStatic || s.dir != dir || s.PickerDir() != dir {
		t.Fatalf("static dir wiring: static=%v mode=%v dir=%q picker=%q", s.static != nil, s.Mode(), s.dir, s.PickerDir())
	}
	if s.path != newer || s.cfg.Path != newer {
		t.Fatalf("opened %q (cfg %q), want newest %q", s.path, s.cfg.Path, newer)
	}
}

func TestOpenStdinReadsFromReader(t *testing.T) {
	cfg := openCfg("")
	cfg.Stdin = true
	s, err := NewSessionFromReader(cfg, strings.NewReader(logText(12)))
	if err != nil {
		t.Fatal(err)
	}
	if !s.stdin || s.Mode() != ModeLive || s.ring == nil || s.tailer == nil || s.path != "stdin" {
		t.Fatalf("stdin wiring: stdin=%v mode=%v path=%q", s.stdin, s.Mode(), s.path)
	}
	ctxStart(t, s)
	waitFor(t, "records", func() bool { return s.Status().Records == 12 })
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewSession(openCfg(dir)); err == nil || !strings.Contains(err.Error(), "no matching log files") {
		t.Errorf("empty static directory: %v", err)
	}
	if _, err := NewSession(openCfg(filepath.Join(dir, "missing.log"))); err == nil {
		t.Error("missing static file must fail")
	}
	cfg := openCfg(dir)
	cfg.Glob = "[" // invalid pattern
	if _, err := NewSession(cfg); err == nil {
		t.Error("invalid glob must fail")
	}
	f := writeLog(t, dir, "x.log", logText(2))
	cfg = openCfg(f)
	cfg.Parser = "no-such-parser"
	if _, err := NewSession(cfg); err == nil || !strings.Contains(err.Error(), "unknown parser") {
		t.Errorf("unknown parser: %v", err)
	}
	cfg = openCfg(dir)
	cfg.Follow, cfg.DirMode = true, "sideways"
	if _, err := NewSession(cfg); err == nil {
		t.Error("bad directory mode must fail")
	}
}

func ctxStart(t *testing.T, s *Session) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
}

func openFDs(t *testing.T) int {
	t.Helper()
	des, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc/self/fd on this platform")
	}
	return len(des)
}

// A failed static open must not leak the handle it opened, and a successful one
// must give it back on Close.
func TestStaticOpenDoesNotLeakFileHandles(t *testing.T) {
	dir := t.TempDir()
	f := writeLog(t, dir, "x.log", logText(50))
	base := openFDs(t)
	for i := 0; i < 30; i++ {
		cfg := openCfg(f)
		cfg.Parser = "no-such-parser" // fails after the file is open
		if _, err := NewSession(cfg); err == nil {
			t.Fatal("expected an error")
		}
	}
	if got := openFDs(t); got > base+2 {
		t.Fatalf("failed opens leaked handles: %d open before, %d after", base, got)
	}
	for i := 0; i < 30; i++ {
		s, err := NewSession(openCfg(f))
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	if got := openFDs(t); got > base+2 {
		t.Fatalf("closed sessions leaked handles: %d open before, %d after", base, got)
	}
}

func TestStaticOpenRejectsADirectoryHandle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directories cannot be opened like files on Windows")
	}
	dir := t.TempDir()
	if _, err := openSession(openCfg(dir), kindStaticFile, nil); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("got %v", err)
	}
}

func TestStaticOpenOfAnEmptyFile(t *testing.T) {
	p := writeLog(t, t.TempDir(), "empty.log", "")
	s, err := NewSession(openCfg(p))
	if err != nil {
		t.Fatalf("an empty file must open: %v", err)
	}
	s.Close()
}
