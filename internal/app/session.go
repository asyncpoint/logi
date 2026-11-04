// Package app wires the engine to a front end. It knows nothing about
// terminals; the TUI consumes it through Session.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
	"github.com/asyncpoint/logi/internal/input"
	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/plugins"
	"github.com/asyncpoint/logi/internal/search"
)

// Mode is the operating mode.
type Mode int

const (
	ModeStatic Mode = iota // lazy, offset-based access to a finished file
	ModeLive               // tail -f semantics into a bounded ring buffer
)

func (m Mode) String() string {
	if names := [...]string{"static", "live"}; m >= 0 && int(m) < len(names) {
		return names[m]
	}
	return "unknown"
}

// Status is a point-in-time summary for the status bar.
type Status struct {
	Mode        Mode
	Path        string
	Parser      string
	Records     int // records currently held / indexed
	ViewRows    int // rows in the current (possibly filtered) view
	Stats       engine.StatsSnapshot
	BufferCap   int
	BufferBytes int64
	FileSize    int64
	IndexDone   bool
	IndexBytes  int64
	Source      input.SourceState
	SourceMsg   string
	Dir         string   // directory being monitored or browsed ("" for a single file)
	DirMode     string   // "latest" | "all" (live directories)
	Files       []string // files currently followed (live directories)
	Counts      bool     // static mode: Stats.BySeverity holds the file's severity counts
	Counting    bool     // ... and the scan behind them is still running
	Filter      string
	Filtering   bool // filter still scanning
	Capped      bool
	Search      search.Request
}

// Session owns one opened log and everything attached to it.
type Session struct {
	cfg     config.Config
	mode    Mode
	path    string
	reg     *parser.Registry
	profile *parser.Profile

	stats      *engine.Stats
	ring       *engine.RingStore
	static     *input.StaticStore
	census     *severityCensus // static mode with StaticCounts on, otherwise nil
	censusWake chan struct{}   // nudges censusWorker when the index grows
	tailer     *input.Tailer
	dirf       *input.DirFollower
	dir        string // directory this session monitors/browses, "" for a plain file
	filter     *input.FileFilter
	store      engine.RecordStore

	em     *emitter
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	stMu     sync.RWMutex
	srcState input.SourceState
	srcMsg   string

	stdin bool // reading standard input: there is no file to anchor times on

	view  filteredView
	query searchState
	slots sourceSlots // stable per-file colour slots (multi-file view)

	anchorMu   sync.Mutex // guards the cached newest-file lookup (anchorFile)
	anchorPath string
	anchorAt   time.Time
}

func newSessionBase(cfg config.Config) (*Session, engine.Limits, error) {
	reg, err := plugins.BuildRegistry(cfg.Plugins)
	if err != nil {
		return nil, engine.Limits{}, err
	}
	s := &Session{cfg: cfg, path: cfg.Path, reg: reg, stats: &engine.Stats{}, view: filteredView{wake: make(chan struct{}, 1)},
		em: newEmitter(cfg.BatchInterval)}
	s.mode = ModeStatic
	if cfg.Follow {
		s.mode = ModeLive
	}
	return s, engine.Limits{MaxRecordBytes: cfg.MaxRecordBytes, MaxLines: cfg.MaxLines}, nil
}

func sourceID(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

func (s *Session) pickProfile(sample []byte) (*parser.Profile, error) {
	var p *parser.Profile
	if s.cfg.Parser == "" || s.cfg.Parser == "auto" {
		p = s.reg.Detect(sample)
	} else {
		var ok bool
		if p, ok = s.reg.Get(s.cfg.Parser); !ok {
			return nil, fmt.Errorf("unknown parser %q (available: %v)", s.cfg.Parser, s.reg.Names())
		}
	}
	if s.cfg.Multiline == "off" {
		cp := *p
		cp.NewFramer = func(_ []byte, lim engine.Limits, off int64) framing.Framer { return framing.NewLineFramer(lim, off) }
		p = &cp
	}
	return p, nil
}

// Dir is the directory this session monitors or browses ("" for a plain file).
func (s *Session) Dir() string { return s.dir }

// PickerDir is non-empty when a static session was opened from a directory and
// the UI should offer a file picker.
func (s *Session) PickerDir() string {
	if s.mode == ModeStatic {
		return s.dir
	}
	return ""
}

// MultiSource reports whether records come from several sources at once: files
// followed together, or the services/pods of a piped compose or kubectl stream.
// The view then shows each source in its own colour.
func (s *Session) MultiSource() bool {
	return s.dirf != nil && s.cfg.DirMode == "all" || s.tailer != nil && s.tailer.Sources() > 0
}

// ListDir lists the log files of the session's directory, newest first.
func (s *Session) ListDir() ([]input.FileEntry, error) {
	if s.dir == "" {
		return nil, fmt.Errorf("not a directory session")
	}
	return input.ListFiles(s.dir, s.filter)
}

// Layout returns the list columns of the format in use, in the order the file
// itself defines them (default columns until the format is known, and for
// directories that mix formats).
func (s *Session) Layout() []parser.ColumnSpec {
	var p *parser.Profile
	switch {
	case s.dirf != nil:
		switch n := s.dirf.ProfileName(); n {
		case "":
		case "mixed":
			// Files of different formats: the generic columns plus the columns the
			// member formats mark as worth showing when mixed (e.g. NCache's event).
			return parser.MixedColumns(s.dirf.Profiles())
		default:
			p, _ = s.reg.Get(n)
		}
	case s.tailer != nil:
		p = s.tailer.Profile()
	default:
		p = s.profile
	}
	return p.ColumnLayout()
}

// Registry exposes the parser registry (for --list-parsers).
func (s *Session) Registry() *parser.Registry { return s.reg }

// Mode returns the operating mode.
func (s *Session) Mode() Mode { return s.mode }

// Updates delivers coalesced notifications. It is never closed; stop reading
// when the Session's context ends.
func (s *Session) Updates() <-chan Update { return s.em.out }

// Start launches the background goroutines. ctx cancellation or Close stops them.
func (s *Session) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(2)
	go func() { defer s.wg.Done(); s.em.run(s.ctx) }()
	go func() { defer s.wg.Done(); s.filterWorker() }()
	switch s.mode {
	case ModeLive:
		run := func(ctx context.Context) error { return s.tailer.Run(ctx) }
		if s.dirf != nil {
			run = s.dirf.Run
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if err := run(s.ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.em.with(func(u *Update) { u.Fatal = err })
			}
		}()
	default:
		if s.census != nil {
			s.wg.Add(1)
			go func() { defer s.wg.Done(); s.censusWorker() }()
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if err := s.static.Index(s.ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.em.with(func(u *Update) { u.Fatal = err })
			}
		}()
	}
	if s.view.active() {
		s.wakeFilter()
	}
}

// Close cancels background work, waits for it, and releases the file.
func (s *Session) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.query.Cancel()
	s.wg.Wait()
	if s.static != nil {
		s.static.Close()
	}
}

func (s *Session) staticNotify() {
	scanned, size, done := s.static.Progress()
	s.em.index(IndexProgress{Scanned: scanned, Size: size, Done: done, Records: s.static.Len()})
	s.wakeFilter()
	select { // let the severity count follow the index
	case s.censusWake <- struct{}{}:
	default:
	}
}

func (s *Session) setSourceState(st input.SourceState, msg string) {
	s.stMu.Lock()
	s.srcState, s.srcMsg = st, msg
	s.stMu.Unlock()
}

// ProfileName is the parser in use ("detecting…" until known in live mode).
func (s *Session) ProfileName() string {
	if s.dirf != nil {
		if n := s.dirf.ProfileName(); n != "" {
			return n
		}
		return "detecting…"
	}
	p := s.profile
	if s.tailer != nil {
		p = s.tailer.Profile()
	}
	if p == nil {
		return "detecting…"
	}
	return p.Name
}

// Status summarises the session for display.
func (s *Session) Status() Status {
	st := Status{Mode: s.mode, Path: s.path, Parser: s.ProfileName(), Records: s.store.Len(), Stats: s.stats.Snapshot()}
	s.stMu.RLock()
	st.Source, st.SourceMsg = s.srcState, s.srcMsg
	s.stMu.RUnlock()
	if s.ring != nil {
		st.BufferCap, st.BufferBytes = s.ring.Capacity(), s.ring.Bytes()
	}
	st.Dir = s.dir
	if s.dirf != nil {
		st.DirMode, st.Files = s.cfg.DirMode, s.dirf.Files()
	}
	if s.static != nil {
		st.IndexBytes, st.FileSize, st.IndexDone = s.static.Progress()
	}
	if s.census != nil {
		var done bool
		st.Stats.BySeverity, done = s.census.snapshot()
		st.Counts, st.Counting = true, !done
	}
	var filterActive bool
	st.Filter, filterActive, st.Filtering, st.Capped, st.ViewRows = s.view.status()
	if !filterActive {
		st.ViewRows = st.Records
	}
	st.Search = s.query.Request()
	return st
}
