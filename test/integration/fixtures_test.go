package integration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/search"
)

// Every fixture is opened through the full stack (detect -> frame -> parse).
func TestFixturesDetectAndGroupCorrectly(t *testing.T) {
	cases := []struct {
		file, parser string
		records      int  // logigen wrote 400 logical records
		hasMultiline bool // ERROR records carry a stack trace
	}{
		{"sample-app.log", "generic", 400, true},
		{"sample.jsonl", "json", 400, false},
		{"sample-ncache.log", "ncache", 400, true},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Path = filepath.Join("..", "fixtures", c.file)
			cfg.BatchInterval = 5 * time.Millisecond
			s, err := app.NewSession(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer func() { cancel(); s.Close() }()
			s.Start(ctx)
			go func() {
				for {
					select {
					case <-s.Updates():
					case <-ctx.Done():
						return
					}
				}
			}()
			waitFor(t, "index", 5*time.Second, func() bool { return s.Status().IndexDone })
			if got := s.ProfileName(); got != c.parser {
				t.Fatalf("detected parser %q, want %q", got, c.parser)
			}
			if n := s.ViewLen(); n != c.records {
				t.Fatalf("%d records, want %d (stack traces must fold into their record)", n, c.records)
			}
			multi := 0
			for row := 0; row < s.ViewLen(); row++ {
				rec, _ := s.ViewAt(row)
				if rec.ParseErr {
					t.Fatalf("row %d flagged as parse error: %q", row, rec.Message)
				}
				if rec.Multiline {
					multi++
					if !strings.Contains(rec.Message, "Exception") {
						t.Fatalf("multiline record without its stack trace: %q", rec.Message)
					}
				}
			}
			if c.hasMultiline != (multi > 0) {
				t.Fatalf("multiline records = %d", multi)
			}
			if c.parser == "ncache" {
				var withEx int
				for row := 0; row < s.ViewLen(); row++ {
					if rec, _ := s.ViewAt(row); rec.Fields.GetValue("exception") != "" {
						withEx++
					}
				}
				if withEx != multi {
					t.Fatalf("ncache exception field extracted for %d of %d multiline records", withEx, multi)
				}
			}
		})
	}
}

// The directory fixture holds three NCache daily files of 120 records each.
func TestDirectoryFixture(t *testing.T) {
	dir := filepath.Join("..", "fixtures", "logdir")
	start := func(mod func(*config.Config)) *app.Session {
		cfg := config.Defaults()
		cfg.Path, cfg.BatchInterval, cfg.Backlog = dir, 5*time.Millisecond, -1
		mod(&cfg)
		s, err := app.NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.Start(ctx)
		t.Cleanup(func() { cancel(); s.Close() })
		go func() {
			for {
				select {
				case <-s.Updates():
				case <-ctx.Done():
					return
				}
			}
		}()
		return s
	}
	t.Run("static opens newest and lists all", func(t *testing.T) {
		s := start(func(*config.Config) {})
		waitFor(t, "index", 5*time.Second, func() bool { return s.Status().IndexDone })
		files, _ := s.ListDir()
		if len(files) != 3 || files[0].Name != "cache.2026-09-21.logs.txt" && files[0].Name != "cache.2026-09-20.logs.txt" && files[0].Name != "cache.2026-09-19.logs.txt" {
			t.Fatalf("%+v", files)
		}
		if s.ViewLen() != 120 || s.ProfileName() != "ncache" || s.PickerDir() == "" {
			t.Fatalf("len=%d parser=%s picker=%q", s.ViewLen(), s.ProfileName(), s.PickerDir())
		}
	})
	t.Run("live all follows every daily file", func(t *testing.T) {
		s := start(func(c *config.Config) { c.Follow, c.DirMode = true, "all" })
		waitFor(t, "360 records", 10*time.Second, func() bool { return s.Status().Records == 360 })
		st := s.Status()
		if len(st.Files) != 3 || s.ProfileName() != "ncache" {
			t.Fatalf("files=%v parser=%s", st.Files, s.ProfileName())
		}
		if err := s.SetFilter("source:2026-09-20 severity:error"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "filter", 5*time.Second, func() bool { st := s.Status(); return !st.Filtering && st.ViewRows > 0 })
		for row := 0; row < s.ViewLen(); row++ {
			r, _ := s.ViewAt(row)
			if !strings.Contains(r.SourceID, "2026-09-20") || r.Severity.String() != "ERROR" {
				t.Fatalf("filter leaked %s %v", r.SourceID, r.Severity)
			}
		}
	})
}

// The lines of a real NCache 5.3 SP7 cache log (columnar header + fixed widths):
// every record needs a level, and the header must not become a record.
func TestRealNCacheCacheLog(t *testing.T) {
	cfg := config.Defaults()
	cfg.Path = filepath.Join("..", "fixtures", "sample-ncache-table.log")
	cfg.BatchInterval = 5 * time.Millisecond
	s, err := app.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); s.Close() }()
	s.Start(ctx)
	go func() {
		for {
			select {
			case <-s.Updates():
			case <-ctx.Done():
				return
			}
		}
	}()
	waitFor(t, "index", 5*time.Second, func() bool { return s.Status().IndexDone })
	if s.ProfileName() != "ncache" {
		t.Fatalf("parser %q", s.ProfileName())
	}
	if n := s.ViewLen(); n != 10 {
		t.Fatalf("%d records, want 10 (header skipped, stack trace folded)", n)
	}
	var names []string
	for _, c := range s.Layout() {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "time,appdomain,logger,thread,level,event,source,message" {
		t.Fatalf("layout %s", got)
	}
	counts := map[string]int{}
	for row := 0; row < s.ViewLen(); row++ {
		rec, _ := s.ViewAt(row)
		if rec.Severity.String() == "" || rec.ParseErr {
			t.Fatalf("record %d has no level / parse error: %q", rec.ID, rec.Message)
		}
		counts[rec.Severity.String()]++
	}
	if counts["INFO"] != 5 || counts["ERROR"] != 2 || counts["WARN"] != 1 || counts["FATAL"] != 1 || counts["DEBUG"] != 1 {
		t.Fatalf("level counts %v", counts)
	}
	if err := s.SetFilter("severity:>=error logger=demoCache"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "filter", 5*time.Second, func() bool { st := s.Status(); return !st.Filtering && st.ViewRows == 3 })
}

// The user's own uploaded cache log: tab-separated columns, a header line
// ending in CRLF while every other line ends in a bare LF, and a trailing
// "END" marker. Every real data line must still get a level.
func TestRealUploadedCacheLogFile(t *testing.T) {
	cfg := config.Defaults()
	cfg.Path = filepath.Join("..", "fixtures", "sample-ncache-realfile.log")
	cfg.BatchInterval = 5 * time.Millisecond
	s, err := app.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); s.Close() }()
	s.Start(ctx)
	waitFor(t, "index", 5*time.Second, func() bool { return s.Status().IndexDone })
	if s.ProfileName() != "ncache" {
		t.Fatalf("parser %q", s.ProfileName())
	}
	if n := s.ViewLen(); n != 62 {
		t.Fatalf("%d records, want 62 (header and trailing END are not records)", n)
	}
	counts := map[string]int{}
	for row := 0; row < s.ViewLen(); row++ {
		rec, _ := s.ViewAt(row)
		if rec.Severity.String() == "" || rec.ParseErr {
			t.Fatalf("record %d has no level despite tab-separated columns: %q", rec.ID, rec.Message)
		}
		counts[rec.Severity.String()]++
		if strings.Contains(rec.Message, "END") {
			t.Fatalf("the trailing END marker leaked into a record: %q", rec.Message)
		}
	}
	if counts["INFO"] != 60 || counts["ERROR"] != 2 {
		t.Fatalf("level counts %v, want INFO:60 ERROR:2", counts)
	}
	// search must cover the whole raw entry, not only the parsed message: the
	// appdomain field is never part of Message, only of the raw line.
	req := search.Request{Query: "Alachisoft.NCache.CacheHost"}
	gen, err := s.Search(req, 1, app.Forward, true)
	if err != nil {
		t.Fatal(err)
	}
	res := waitForSearch(t, s, gen)
	if !res.Found || res.ID != 1 {
		t.Fatalf("search across the whole raw entry: %+v", res)
	}
}

// The user's NCache event log: TIMESTAMP/SOURCE/EVENTID/LEVEL/MESSAGE, Windows
// level words ("Information"), a millisecond field with a space after the
// comma, a stray blank line and "END" after the first record.
func TestRealUploadedEventLogFile(t *testing.T) {
	cfg := config.Defaults()
	cfg.Path = filepath.Join("..", "fixtures", "sample-ncache-events.log")
	cfg.BatchInterval = 5 * time.Millisecond
	s, err := app.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); s.Close() }()
	s.Start(ctx)
	go func() {
		for {
			select {
			case <-s.Updates():
			case <-ctx.Done():
				return
			}
		}
	}()
	waitFor(t, "index", 5*time.Second, func() bool { return s.Status().IndexDone })
	if s.ProfileName() != "ncache-events" {
		t.Fatalf("parser %q, want ncache-events", s.ProfileName())
	}
	if n := s.ViewLen(); n != 9 {
		t.Fatalf("%d records, want 9", n)
	}
	var names []string
	for _, c := range s.Layout() {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "time,component,eventid,level,source,message" {
		t.Fatalf("layout %s", got)
	}
	for row := 0; row < s.ViewLen(); row++ {
		rec, _ := s.ViewAt(row)
		if rec.Severity.String() != "INFO" || rec.ParseErr {
			t.Fatalf("record %d: severity=%v parseErr=%v %q", rec.ID, rec.Severity, rec.ParseErr, rec.Message)
		}
		if rec.Fields.GetValue("component") == "" || rec.Fields.GetValue("eventid") == "" {
			t.Fatalf("record %d missing component/eventid: %+v", rec.ID, rec.Fields)
		}
	}
	first, _ := s.ViewAt(0)
	if first.Fields.GetValue("component") != "NCache" || first.Fields.GetValue("eventid") != "1020" {
		t.Fatalf("first record fields: %+v", first.Fields)
	}
	if err := s.SetFilter("component=ExecutionService"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "filter", 5*time.Second, func() bool { st := s.Status(); return !st.Filtering && st.ViewRows == 1 })
}

func waitForSearch(t *testing.T, s *app.Session, gen uint64) app.SearchResult {
	t.Helper()
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
