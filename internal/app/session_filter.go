package app

// Filtering: applying a filter query and the background worker that keeps the view current.

import (
	"context"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/filter"
	"github.com/asyncpoint/logi/internal/input"
)

// ---- filtering ----------------------------------------------------------

const anchorTTL = time.Second

// anchorFile is the file whose newest record anchors relative times ("since 15m").
func (s *Session) anchorFile() string {
	switch {
	case s.stdin:
		return ""
	case s.dirf != nil:
		// Only the newest file matters, and a filter can be recompiled in quick
		// succession; avoid re-listing a large directory each time.
		s.anchorMu.Lock()
		defer s.anchorMu.Unlock()
		if time.Since(s.anchorAt) < anchorTTL && s.anchorPath != "" {
			return s.anchorPath
		}
		files, err := input.ListFiles(s.dir, s.filter)
		if err != nil || len(files) == 0 {
			return ""
		}
		s.anchorPath, s.anchorAt = files[0].Path, time.Now() // newest first
		return s.anchorPath
	}
	return s.path
}

// timeAnchor reports the wall-clock reading of the newest record, read from the
// end of the file on first use, so "--since 15m" means the last 15 minutes *of
// the log*, which also works for old files and logs without a time zone. With no
// usable file (standard input) it reports false and the local clock is used.
func (s *Session) timeAnchor() (int64, bool) {
	p := s.anchorFile()
	if p == "" {
		return 0, false
	}
	return input.LastWallNano(p, s.reg, s.cfg.Parser)
}

// SetFilter compiles and applies a filter query ("" clears it).
func (s *Session) SetFilter(q string) error { return s.applyFilter(q) }

func (s *Session) applyFilter(q string) error {
	pred, err := filter.CompileWith(q, filter.Options{Anchor: s.timeAnchor})
	if err != nil {
		return err
	}
	s.view.begin(q, pred, s.mode == ModeLive)
	if pred == nil {
		s.em.filter(FilterStatus{})
	}
	s.wakeFilter()
	return nil
}

func (s *Session) wakeFilter() {
	select {
	case s.view.wake <- struct{}{}:
	default:
	}
}

func (s *Session) filterWorker() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.view.wake:
		}
		s.filterCatchUp()
	}
}

func (s *Session) indexDone() bool {
	if s.static == nil {
		return true
	}
	_, _, d := s.static.Progress()
	return d
}

// filterCatchUp scans forward from the last processed ID until it reaches the
// newest record. Live appends and rebuilds use the same loop, so incremental
// filtering is just "catch up from where we left off".
func (s *Session) filterCatchUp() {
	// Static indexing can scan large segments efficiently. Live filtering uses
	// smaller batches so each pass holds the ring read lock only briefly and
	// does not noticeably delay ingestion.
	step := uint64(256)
	if s.static != nil {
		step = 1 << 16
	}
	var (
		keep     func(string) bool // raw prefilter, derived once per filter generation
		keepGen  uint64
		haveKeep bool
	)
	for s.ctx.Err() == nil {
		pred, gen, from := s.view.cursor()
		if pred == nil {
			return
		}
		if !haveKeep || keepGen != gen {
			keep, keepGen, haveKeep = filter.RawPrefilter(pred), gen, true
		}
		oldest, newest := s.store.OldestID(), s.store.NewestID()
		if from < oldest {
			from = oldest
		}
		if newest == 0 || from > newest {
			s.finishFilter(gen)
			return
		}
		end := from + step - 1
		if end > newest {
			end = newest
		}
		matches := make([]uint64, 0, min(int(step), 1024))
		// Only records accepted by pred.Match reach the callback.
		s.rangeMatching(s.ctx, from, end, keep, pred.Match, func(r *engine.LogRecord) bool {
			matches = append(matches, r.ID)
			return true
		})
		accepted, capped := s.view.commit(gen, matches, end, oldest, s.cfg.MaxFilterMatches)
		if !accepted { // superseded while scanning: discard this batch
			continue
		}
		s.emitFilter()
		if capped {
			return
		}
	}
}

// rangeMatching scans [from,to]. Stores that support it skip parsing records
// the raw prefilter rejects and evaluate accept on worker goroutines, so only
// matching records reach fn. Other stores get an equivalent sequential scan.
func (s *Session) rangeMatching(ctx context.Context, from, to uint64, keep func(string) bool, accept func(*engine.LogRecord) bool, fn func(*engine.LogRecord) bool) {
	if mr, ok := s.store.(engine.MatchRanger); ok && (keep != nil || accept != nil) {
		mr.RangeMatching(ctx, from, to, keep, accept, fn)
		return
	}
	n := 0
	s.store.Range(from, to, func(r *engine.LogRecord) bool {
		if n++; n&255 == 0 && ctx.Err() != nil {
			return false
		}
		if accept != nil && !accept(r) {
			return true
		}
		return fn(r)
	})
}

// withStartupFilter applies the startup filter. On error the half-built session
// is closed (releasing its file handles) and nil is returned, so callers never
// receive a session alongside an error.
func (s *Session) withStartupFilter(q string) (*Session, error) {
	if err := s.applyFilter(q); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) finishFilter(gen uint64) {
	s.view.finish(gen, s.indexDone())
	s.emitFilter()
}

func (s *Session) emitFilter() { s.em.filter(s.view.filterStatus()) }
