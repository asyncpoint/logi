package app

import (
	"context"
	"sync"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/input"
)

// Update is one coalesced notification for the UI. Many source
// events collapse into a single Update per batch interval; the UI then reads
// whatever state it needs from the Session.
type Update struct {
	Added           int    // records added since the previous Update
	FirstID, LastID uint64 // range of added IDs
	Evicted         int
	State           *StateChange
	Index           *IndexProgress
	Filter          *FilterStatus
	Search          *SearchResult
	Counts          bool // static mode: the severity counts changed
	Fatal           error
}

// StateChange reports live-source state transitions.
type StateChange struct {
	State input.SourceState
	Msg   string
}

// IndexProgress reports static-file indexing progress.
type IndexProgress struct {
	Scanned, Size int64
	Done          bool
	Records       int
}

// FilterStatus reports incremental filter progress.
type FilterStatus struct {
	Query    string
	Building bool
	Matches  int
	Capped   bool
}

// SearchResult is the outcome of an asynchronous search.
type SearchResult struct {
	Gen      uint64
	Found    bool
	ID       uint64 // retained for row navigation compatibility
	Position engine.SourcePosition
	Wrapped  bool
	Query    string
	Err      error
}

// emitter accumulates events and delivers them at most once per interval.
// Producers never block: only the emitter goroutine waits on the UI, and
// while it waits, further events keep merging into the pending Update.
type emitter struct {
	mu       sync.Mutex
	p        Update
	dirty    bool
	interval time.Duration
	wake     chan struct{}
	out      chan Update
}

func newEmitter(interval time.Duration) *emitter {
	return &emitter{interval: interval, wake: make(chan struct{}, 1), out: make(chan Update, 1)}
}

func (e *emitter) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *emitter) added(first, last uint64, evicted int) {
	e.mu.Lock()
	if e.p.Added == 0 {
		e.p.FirstID = first
	}
	e.p.LastID = last
	e.p.Added += int(last-first) + 1
	e.p.Evicted += evicted
	e.dirty = true
	e.mu.Unlock()
	e.signal()
}

func (e *emitter) with(fn func(u *Update)) {
	e.mu.Lock()
	fn(&e.p)
	e.dirty = true
	e.mu.Unlock()
	e.signal()
}

func (e *emitter) state(s input.SourceState, msg string) {
	e.with(func(u *Update) { u.State = &StateChange{s, msg} })
}

func (e *emitter) index(p IndexProgress) { e.with(func(u *Update) { u.Index = &p }) }
func (e *emitter) filter(f FilterStatus) { e.with(func(u *Update) { u.Filter = &f }) }
func (e *emitter) search(r SearchResult) { e.with(func(u *Update) { u.Search = &r }) }

func (e *emitter) run(ctx context.Context) {
	// Reuse one timer rather than allocating a new time.After timer for every
	// burst of source notifications.
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		}
		// Batch: give bursts a moment to merge into one Update. Reusing the
		// timer avoids per-burst timer allocations under heavy log traffic.
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(e.interval)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		e.mu.Lock()
		u, ok := e.p, e.dirty
		e.p, e.dirty = Update{}, false
		e.mu.Unlock()
		if !ok {
			continue
		}
		select {
		case e.out <- u:
		case <-ctx.Done():
			return
		}
	}
}

var _ input.Sink = (*sessionSink)(nil)

type sessionSink struct{ s *Session }

func (k *sessionSink) Added(first, last uint64, evicted int) {
	k.s.em.added(first, last, evicted)
	k.s.wakeFilter()
}

func (k *sessionSink) State(st input.SourceState, msg string) {
	k.s.setSourceState(st, msg)
	k.s.em.state(st, msg)
}
