package app

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/input"
	"github.com/asyncpoint/logi/internal/platform"
)

// sourceKind is the shape of input a session was asked to open. There are five,
// and each wires up a different set of Session fields; keeping that wiring in
// one small builder per kind means a mode can be read, tested and changed on
// its own, and a new kind is one more entry in openers.
type sourceKind int

const (
	kindStdin      sourceKind = iota // a pipe, followed live into the ring buffer
	kindLiveFile                     // one file, tail -f into the ring buffer
	kindLiveDir                      // a directory of files followed together
	kindStaticDir                    // a directory browsed; its newest file opened static
	kindStaticFile                   // one finished file, indexed and read lazily
)

func (k sourceKind) String() string {
	if names := [...]string{"stdin", "live file", "live directory", "static directory", "static file"}; k >= 0 && int(k) < len(names) {
		return names[k]
	}
	return "unknown"
}

// openers maps each kind to the function that wires it into a Session. It is the
// only place that knows which fields a kind sets.
var openers = map[sourceKind]func(s *Session, lim engine.Limits, r io.Reader) error{
	kindStdin:      (*Session).openStdin,
	kindLiveFile:   (*Session).openLiveFile,
	kindLiveDir:    (*Session).openLiveDir,
	kindStaticDir:  (*Session).openStaticDir,
	kindStaticFile: (*Session).openStaticFile,
}

// detectKind decides which kind of input cfg describes. It is the single place
// that looks at the file system to choose a mode. A path that cannot be
// inspected is treated as a file: a live tail may wait for it to appear, and a
// static open reports the error with context.
func detectKind(cfg config.Config) sourceKind {
	if cfg.Stdin && cfg.Path == "" {
		return kindStdin
	}
	isDir := false
	if fi, err := os.Stat(cfg.Path); err == nil && fi.IsDir() {
		isDir = true
	}
	switch {
	case cfg.Follow && isDir:
		return kindLiveDir
	case cfg.Follow:
		return kindLiveFile
	case isDir:
		return kindStaticDir
	}
	return kindStaticFile
}

// NewSessionFromReader follows r (standard input) live: the bytes go through the
// same framing, parsing and filtering as a file, into the bounded ring buffer.
func NewSessionFromReader(cfg config.Config, r io.Reader) (*Session, error) {
	cfg.Follow, cfg.Path = true, "stdin"
	return openSession(cfg, kindStdin, r)
}

// NewSession opens the file, directory or standard input described by cfg but
// does not start background work until Start.
func NewSession(cfg config.Config) (*Session, error) {
	kind := detectKind(cfg)
	if kind == kindStdin {
		return NewSessionFromReader(cfg, os.Stdin)
	}
	return openSession(cfg, kind, nil)
}

func openSession(cfg config.Config, kind sourceKind, r io.Reader) (*Session, error) {
	s, lim, err := newSessionBase(cfg)
	if err != nil {
		return nil, err
	}
	if kind == kindLiveDir || kind == kindStaticDir {
		// Both directory kinds select files by the same glob.
		if s.filter, err = input.NewFileFilter(cfg.Glob); err != nil {
			return nil, err
		}
		s.dir = cfg.Path
	}
	if err := openers[kind](s, lim, r); err != nil {
		return nil, err
	}
	return s.withStartupFilter(s.cfg.Filter)
}

func (s *Session) newRing() {
	s.ring = engine.NewRingStore(s.cfg.Buffer, s.cfg.BufferBytes, s.stats)
	s.store = s.ring
}

func (s *Session) openStdin(lim engine.Limits, r io.Reader) error {
	s.stdin = true
	s.newRing()
	t, err := input.NewTailer(input.TailOptions{
		SourceID: "stdin", Registry: s.reg, Parser: s.cfg.Parser, Limits: lim,
		Store: s.ring, Sink: &sessionSink{s}, IdleFlush: s.cfg.IdleFlush,
		LineFraming: s.cfg.Multiline == "off", Reader: r,
	})
	if err != nil {
		return err
	}
	s.tailer = t
	return nil
}

func (s *Session) openLiveFile(lim engine.Limits, _ io.Reader) error {
	cfg := s.cfg
	s.newRing()
	t, err := input.NewTailer(input.TailOptions{
		Path: cfg.Path, SourceID: sourceID(cfg.Path), Registry: s.reg, Parser: cfg.Parser, Limits: lim,
		Store: s.ring, Sink: &sessionSink{s}, Backlog: cfg.Backlog, IdleFlush: cfg.IdleFlush,
		BacklogLines: cfg.Lines, UseBacklogLines: cfg.LinesSet,
		ForcePoll: cfg.ForcePoll, LineFraming: cfg.Multiline == "off",
	})
	if err != nil {
		return err
	}
	s.tailer = t
	return nil
}

func (s *Session) openLiveDir(lim engine.Limits, _ io.Reader) error {
	cfg := s.cfg
	mode, err := input.ParseDirMode(cfg.DirMode)
	if err != nil {
		return err
	}
	s.newRing()
	d, err := input.NewDirFollower(input.DirOptions{Dir: cfg.Path, Filter: s.filter, Mode: mode, MaxFiles: cfg.MaxFiles,
		Tail: input.TailOptions{Registry: s.reg, Parser: cfg.Parser, Limits: lim, Store: s.ring, Sink: &sessionSink{s},
			Backlog: cfg.Backlog, BacklogLines: cfg.Lines, UseBacklogLines: cfg.LinesSet, IdleFlush: cfg.IdleFlush, ForcePoll: cfg.ForcePoll, LineFraming: cfg.Multiline == "off"}})
	if err != nil {
		return err
	}
	s.dirf = d
	if files, err := input.ListFiles(cfg.Path, s.filter); err == nil {
		names := make([]string, 0, len(files))
		for i, f := range files { // newest first; only the files that will be followed
			if i >= cfg.MaxFiles && cfg.MaxFiles > 0 {
				break
			}
			names = append(names, f.Name)
		}
		s.slots.seed(names)
	}
	return nil
}

// openStaticDir opens the newest matching file of a directory; the UI offers a
// picker to switch to another one.
func (s *Session) openStaticDir(lim engine.Limits, r io.Reader) error {
	files, err := input.ListFiles(s.cfg.Path, s.filter)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no matching log files in %s", s.cfg.Path)
	}
	s.path, s.cfg.Path = files[0].Path, files[0].Path
	return s.openStaticFile(lim, r)
}

func (s *Session) openStaticFile(lim engine.Limits, _ io.Reader) error {
	path := s.cfg.Path
	// One handle serves everything: the type check, the sample that picks the
	// parser and seeds the framer, and then the store itself. platform.Open, like
	// the rest of the file access, lets the file be rotated or deleted while it
	// is read on Windows.
	f, err := platform.Open(path)
	if err != nil {
		return config.PathError(path, err)
	}
	st, err := s.openStaticHandle(f, path, lim)
	if err != nil {
		f.Close() // the store only takes the handle on success
		return err
	}
	s.static, s.store = st, st
	if s.cfg.StaticCounts {
		s.census, s.censusWake = &severityCensus{}, make(chan struct{}, 1)
	}
	return nil
}

func (s *Session) openStaticHandle(f *os.File, path string, lim engine.Limits) (*input.StaticStore, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, config.PathError(path, err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	sample := make([]byte, 64<<10)
	n, rerr := f.ReadAt(sample, 0) // does not move the file offset
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return nil, rerr
	}
	sample = sample[:n]
	prof, err := s.pickProfile(sample)
	if err != nil {
		return nil, err
	}
	st, err := input.OpenStatic(input.StaticOptions{Path: path, File: f, SourceID: sourceID(path), Profile: prof,
		Sample: sample, Limits: lim, Notify: s.staticNotify})
	if err != nil {
		return nil, err
	}
	s.profile = prof
	return st, nil
}
