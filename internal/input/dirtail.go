package input

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/platform"
)

// DirMode selects how a directory is followed.
type DirMode int

const (
	// DirLatest follows the newest file and moves to the next one when the
	// application rolls over (e.g. cache.2026-09-20.log).
	DirLatest DirMode = iota
	// DirAll follows every matching file at once.
	DirAll
)

func (m DirMode) String() string { return [...]string{"latest", "all"}[m] }

// ParseDirMode parses "latest" or "all".
func ParseDirMode(s string) (DirMode, error) {
	switch s {
	case "", "latest":
		return DirLatest, nil
	case "all":
		return DirAll, nil
	}
	return 0, fmt.Errorf("directory mode must be latest or all, got %q", s)
}

// DirOptions configures a DirFollower.
type DirOptions struct {
	Dir          string
	Filter       *FileFilter
	Mode         DirMode
	MaxFiles     int           // DirAll: at most this many files followed at once (default 32)
	Rescan       time.Duration // safety rescan period (default 5s with notifications, 500ms without)
	SilentSwitch time.Duration // DirLatest: how long the current file must be silent before a merely-newer file replaces it (default 5s)
	Tail         TailOptions   // template for per-file tailers (Path/SourceID/Sink/Wake are set per file)
}

type followed struct {
	path, name string
	t          *Tailer
	wake       chan struct{}
	done       chan struct{}
	missing    int
}

// DirFollower monitors a directory and feeds every followed file into one
// shared ring buffer. Every file has its own Tailer, so rotation,
// truncation and partial-line handling behave exactly as for a single file.
type DirFollower struct {
	o   DirOptions
	ctx context.Context

	mu        sync.Mutex
	files     map[string]*followed
	seen      map[platform.FileIdentity]struct{}
	seenOrder []platform.FileIdentity
	started   bool
	watching  bool
	waiting   bool

	scanWake chan struct{}
	wg       sync.WaitGroup
}

const maxSeenIdentities = 4096

// NewDirFollower validates options.
func NewDirFollower(o DirOptions) (*DirFollower, error) {
	if o.Tail.Store == nil || o.Tail.Registry == nil || o.Tail.Sink == nil {
		return nil, fmt.Errorf("input: directory follower requires Store, Registry and Sink")
	}
	fi, err := os.Stat(o.Dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", o.Dir)
	}
	if o.Filter == nil {
		o.Filter, _ = NewFileFilter("")
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = 32
	}
	if o.SilentSwitch <= 0 {
		o.SilentSwitch = 5 * time.Second
	}
	o.Dir = filepath.Clean(o.Dir)
	return &DirFollower{o: o, files: map[string]*followed{}, seen: map[platform.FileIdentity]struct{}{},
		scanWake: make(chan struct{}, 1)}, nil
}

// Count returns how many files are currently followed.
func (d *DirFollower) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.files)
}

// Files returns the base names of the files currently followed.
func (d *DirFollower) Files() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.files))
	for _, f := range d.files {
		out = append(out, f.name)
	}
	sort.Strings(out)
	return out
}

// ProfileName is the parser of the followed files: their common name, "mixed"
// when they differ, or "" until a format has been detected.
func (d *DirFollower) ProfileName() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := ""
	for _, f := range d.files {
		if p := f.t.Profile(); p != nil {
			if name != "" && name != p.Name {
				return "mixed"
			}
			name = p.Name
		}
	}
	return name
}

// Profiles returns the distinct formats detected among the followed files,
// sorted by name (files whose format is not detected yet are skipped).
func (d *DirFollower) Profiles() []*parser.Profile {
	d.mu.Lock()
	defer d.mu.Unlock()
	byName := map[string]*parser.Profile{}
	for _, f := range d.files {
		if p := f.t.Profile(); p != nil {
			byName[p.Name] = p
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*parser.Profile, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out
}

func (d *DirFollower) pokeScan() {
	select {
	case d.scanWake <- struct{}{}:
	default:
	}
}

func (d *DirFollower) onEvent(ev fsnotify.Event) {
	d.mu.Lock()
	f := d.files[filepath.Clean(ev.Name)]
	d.mu.Unlock()
	if f != nil {
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
	if ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		d.pokeScan()
	}
}

// Run follows the directory until ctx is cancelled.
func (d *DirFollower) Run(ctx context.Context) error {
	d.ctx = ctx
	rescan := d.o.Rescan
	if !d.o.Tail.ForcePoll {
		if fw, err := fsnotify.NewWatcher(); err == nil {
			if err := fw.Add(d.o.Dir); err == nil {
				d.watching = true
				done := make(chan struct{})
				go func() {
					defer close(done)
					for {
						select {
						case ev, ok := <-fw.Events:
							if !ok {
								return
							}
							d.onEvent(ev)
						case _, ok := <-fw.Errors:
							if !ok {
								return
							}
							d.pokeScan()
						}
					}
				}()
				defer func() { fw.Close(); <-done }()
			} else {
				fw.Close()
			}
		}
	}
	if rescan <= 0 {
		rescan = 500 * time.Millisecond
		if d.watching {
			rescan = 5 * time.Second
		}
	}
	tick := time.NewTicker(rescan)
	defer tick.Stop()

	d.scan()
	for {
		select {
		case <-ctx.Done():
			d.wg.Wait() // tailers stop on the same context
			return ctx.Err()
		case <-d.scanWake:
			d.scan()
		case <-tick.C:
			d.scan()
		}
	}
}

// ---- bookkeeping --------------------------------------------------------

func (d *DirFollower) markSeen(id platform.FileIdentity) {
	if id.IsZero() {
		return
	}
	if _, ok := d.seen[id]; ok {
		return
	}
	d.seen[id] = struct{}{}
	d.seenOrder = append(d.seenOrder, id)
	if len(d.seenOrder) > maxSeenIdentities { // bounded memory
		delete(d.seen, d.seenOrder[0])
		d.seenOrder = d.seenOrder[1:]
	}
}

func (d *DirFollower) isSeen(id platform.FileIdentity) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.seen[id]
	return ok
}

// refreshSeen records the current identity of every followed path, so a file
// that is later renamed (rotation) is recognised instead of re-read.
func (d *DirFollower) refreshSeen() {
	d.mu.Lock()
	paths := make([]string, 0, len(d.files))
	for p := range d.files {
		paths = append(paths, p)
	}
	d.mu.Unlock()
	for _, p := range paths {
		if info, err := platform.StatPath(p); err == nil {
			d.mu.Lock()
			d.markSeen(info.ID)
			d.mu.Unlock()
		}
	}
}

// rotatedAway reports whether path holds a file we already followed under a
// different name — a renamed rotation leftover that must not be read again.
func (d *DirFollower) rotatedAway(path string, id platform.FileIdentity) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[id]; !ok {
		return false
	}
	_, following := d.files[path]
	return !following
}

type prefixSink struct {
	base Sink
	name string
}

func (p prefixSink) Added(first, last uint64, evicted int) { p.base.Added(first, last, evicted) }
func (p prefixSink) State(s SourceState, msg string) {
	if msg != "" {
		msg = p.name + ": " + msg
	}
	p.base.State(s, msg)
}

func (d *DirFollower) start(e FileEntry, initial bool) {
	info, err := platform.StatPath(e.Path)
	if err != nil {
		return
	}
	opts := d.o.Tail
	opts.Path, opts.SourceID = e.Path, e.Name
	opts.Sink = prefixSink{d.o.Tail.Sink, e.Name}
	if !initial {
		opts.Backlog = -1 // a file that appeared while we watch is read from its first byte
		opts.UseBacklogLines = false
	}
	f := &followed{path: e.Path, name: e.Name, wake: make(chan struct{}, 1), done: make(chan struct{})}
	if d.watching {
		opts.Wake = f.wake
	} else {
		opts.ForcePoll = true
	}
	t, err := NewTailer(opts)
	if err != nil {
		d.o.Tail.Sink.State(StateError, err.Error())
		return
	}
	f.t = t
	d.mu.Lock()
	d.files[e.Path] = f
	d.markSeen(info.ID)
	d.waiting = false
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		t.Run(d.ctx)
		close(f.done)
	}()
}

// stop drains a tailer completely and forgets it.
func (d *DirFollower) stop(f *followed) {
	f.t.Finish()
	<-f.done
	d.mu.Lock()
	delete(d.files, f.path)
	d.mu.Unlock()
}

func (d *DirFollower) scan() {
	entries, err := ListFiles(d.o.Dir, d.o.Filter)
	if err != nil {
		d.o.Tail.Sink.State(StateError, err.Error())
		return
	}
	d.refreshSeen()
	if d.o.Mode == DirAll {
		d.scanAll(entries)
	} else {
		d.scanLatest(entries)
	}
	if len(entries) == 0 {
		d.mu.Lock()
		report := !d.waiting && len(d.files) == 0
		d.waiting = true
		d.mu.Unlock()
		if report {
			d.o.Tail.Sink.State(StateWaiting, "no matching files in "+d.o.Dir)
		}
	}
}

func (d *DirFollower) current() *followed {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, f := range d.files { // DirLatest follows at most one file
		return f
	}
	return nil
}

func (d *DirFollower) scanLatest(entries []FileEntry) {
	var cand *FileEntry
	var candID platform.FileIdentity
	for i := range entries {
		info, err := platform.StatPath(entries[i].Path)
		if err != nil || d.rotatedAway(entries[i].Path, info.ID) {
			continue
		}
		cand, candID = &entries[i], info.ID
		break
	}
	if cand == nil {
		return
	}
	cur := d.current()
	if cur == nil {
		d.start(*cand, !d.started)
		d.started = true
		return
	}
	if cand.Path == cur.path {
		return
	}
	curInfo, err := os.Stat(cur.path)
	gone := err != nil
	isNew := !d.isSeen(candID) // never followed before: a genuine rollover
	silent := err == nil && time.Since(curInfo.ModTime()) >= d.o.SilentSwitch && cand.ModTime.After(curInfo.ModTime())
	if gone || isNew || silent {
		d.stop(cur) // drain the old file fully so record order is preserved
		d.start(*cand, false)
		d.o.Tail.Sink.State(StateRotated, "now following "+cand.Name)
	}
}

func (d *DirFollower) scanAll(entries []FileEntry) {
	mtime := make(map[string]time.Time, len(entries))
	for _, e := range entries {
		mtime[e.Path] = e.ModTime
	}
	d.mu.Lock()
	var gone []*followed
	for p, f := range d.files {
		if _, ok := mtime[p]; ok {
			f.missing = 0
		} else if f.missing++; f.missing >= 2 { // tolerate a rename/recreate gap
			gone = append(gone, f)
		}
	}
	d.mu.Unlock()
	for _, f := range gone {
		d.stop(f)
	}
	for _, e := range entries { // newest first
		d.mu.Lock()
		_, following := d.files[e.Path]
		n := len(d.files)
		d.mu.Unlock()
		if following {
			continue
		}
		info, err := platform.StatPath(e.Path)
		if err != nil || d.rotatedAway(e.Path, info.ID) {
			continue
		}
		if n >= d.o.MaxFiles {
			lru := d.leastRecent(mtime)
			if lru == nil || !e.ModTime.After(mtime[lru.path]) {
				break // entries are newest-first: nothing further qualifies
			}
			d.stop(lru)
		}
		d.start(e, !d.started)
	}
	d.started = true
}

func (d *DirFollower) leastRecent(mtime map[string]time.Time) *followed {
	d.mu.Lock()
	defer d.mu.Unlock()
	var lru *followed
	for p, f := range d.files {
		if lru == nil || mtime[p].Before(mtime[lru.path]) {
			lru = f
		}
	}
	return lru
}
