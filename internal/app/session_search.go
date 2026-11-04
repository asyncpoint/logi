package app

// Search: literal/regex/field search over view rows, in both directions.

import (
	"context"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/search"
)

// ---- search -------------------------------------------------------------

// Direction of a search.
type Direction int

const (
	Forward Direction = iota
	Backward
)

// Matcher returns the matcher of the latest successful search (for highlights).
func (s *Session) Matcher() *search.Matcher {
	return s.query.Matcher()
}

// Search starts an asynchronous search over the current view, beginning at
// record fromID (exclusive unless inclusive). A newer search cancels the
// previous one; stale results are recognisable by SearchResult.Gen.
func (s *Session) Search(req search.Request, fromID uint64, dir Direction, inclusive bool) (uint64, error) {
	m, err := search.Compile(req)
	if err != nil {
		return 0, err
	}
	ctx, gen := s.query.Start(s.ctx, m)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		pos, found, wrapped := s.findFrom(ctx, m, fromID, dir, inclusive)
		if ctx.Err() != nil {
			return
		}
		id := pos.RecordID
		s.em.search(SearchResult{Gen: gen, Found: found, ID: id, Position: pos, Wrapped: wrapped, Query: req.Query})
	}()
	return gen, nil
}

// positionForRecord creates a payload-free locator directly from a record
// already loaded by the search traversal. This avoids a second store lookup
// after a match is found.
func (s *Session) positionForRecord(r *engine.LogRecord) engine.SourcePosition {
	if r == nil {
		return engine.SourcePosition{}
	}
	sid := r.SourceID
	if sid == "" {
		sid = sourceID(s.path)
	}
	if s.mode == ModeLive {
		return engine.SourcePosition{SourceID: sid, Kind: engine.PositionLiveID, RecordID: r.ID}
	}
	if r.Raw.Length <= 0 {
		return engine.SourcePosition{}
	}
	return engine.SourcePosition{SourceID: sid, Kind: engine.PositionStaticOffset, RecordID: r.ID, Offset: r.Raw.Offset, Length: r.Raw.Length}
}

// scanRows returns the first matching source position while walking view rows
// in the requested direction. It is cancelable through ctx.
func (s *Session) scanRows(ctx context.Context, m *search.Matcher, from, to int, dir Direction) (engine.SourcePosition, bool) {
	if from > to || from < 0 {
		return engine.SourcePosition{}, false
	}
	checked := 0
	cancelled := func() bool { checked++; return checked&255 == 0 && ctx.Err() != nil }

	// try tests one record by ID. matched ends the scan even when the record has
	// no stable position (found is then false), mirroring the single-hit contract.
	try := func(id uint64) (pos engine.SourcePosition, matched bool) {
		rec, ok := s.store.Get(id)
		if !ok {
			return engine.SourcePosition{}, false
		}
		// Cached static records carry no raw text; match what the unfiltered
		// scan matches (the whole record, escapes stripped) so results agree.
		if rec.Raw.Data == "" && rec.Raw.Length > 0 && m.NeedsRaw() {
			if raw, err := s.store.Raw(&rec); err == nil {
				rec.Raw.Data = engine.StripANSI(raw)
			}
		}
		if !m.Match(&rec) {
			return engine.SourcePosition{}, false
		}
		return s.positionForRecord(&rec), true
	}
	none := engine.SourcePosition{}

	if s.view.active() {
		n := to - from + 1
		for i := 0; i < n; i++ {
			row := from + i
			if dir == Backward {
				row = to - i
			}
			id, ok := s.IDAt(row)
			if !ok || cancelled() {
				return none, false
			}
			if pos, matched := try(id); matched {
				return pos, !pos.IsZero()
			}
		}
		return none, false
	}

	lo, ok1 := s.IDAt(from)
	hi, ok2 := s.IDAt(to)
	if !ok1 || !ok2 {
		return none, false
	}

	// Live scans acquire the ring lock for one record at a time. Static scans
	// retain the efficient sequential Range path and capture the position from
	// the matching record itself.
	if s.ring != nil {
		for i, n := uint64(0), hi-lo+1; i < n; i++ {
			id := lo + i
			if dir == Backward {
				id = hi - i
			}
			if cancelled() {
				return none, false
			}
			if pos, matched := try(id); matched {
				return pos, !pos.IsZero()
			}
		}
		return none, false
	}

	var found engine.SourcePosition
	if dir == Forward {
		s.rangeMatching(ctx, lo, hi, m.RawKeep(), m.Match, func(r *engine.LogRecord) bool {
			if cancelled() {
				return false
			}
			// Only records already accepted by m.Match reach this callback.
			found = s.positionForRecord(r)
			return false
		})
		return found, !found.IsZero()
	}
	// Backward static search walks fixed windows from the end. The last match
	// in the first window containing a match is the nearest one.
	// Windows are sized in bytes (about one read segment per scan worker) so
	// each one can use the parallel scan path; a fixed record count rarely
	// spanned more than one segment.
	window := uint64(4096)
	if s.static != nil {
		window = s.static.ScanWindow(window)
	}
	for h := hi; h >= lo; {
		l := lo
		if h-lo+1 > window {
			l = h - window + 1
		}
		s.rangeMatching(ctx, l, h, m.RawKeep(), m.Match, func(r *engine.LogRecord) bool {
			if cancelled() {
				return false
			}
			found = s.positionForRecord(r) // accepted by m.Match; keep the last
			return true
		})
		if !found.IsZero() || ctx.Err() != nil || l == lo {
			return found, !found.IsZero()
		}
		h = l - 1
	}
	return engine.SourcePosition{}, false
}

func (s *Session) findFrom(ctx context.Context, m *search.Matcher, fromID uint64, dir Direction, inclusive bool) (engine.SourcePosition, bool, bool) {
	n := s.ViewLen()
	if n == 0 {
		return engine.SourcePosition{}, false, false
	}
	cur := s.RowOf(fromID)
	if cur < 0 {
		cur = 0
	}
	rid, _ := s.IDAt(cur)
	var first, last int
	switch {
	case rid == fromID:
		first, last = cur, cur
		if !inclusive {
			first, last = cur+1, cur-1
		}
	case rid > fromID:
		first, last = cur, cur-1
	default:
		first, last = cur+1, cur
	}
	if dir == Forward {
		if pos, ok := s.scanRows(ctx, m, first, n-1, Forward); ok {
			return pos, true, false
		}
		if pos, ok := s.scanRows(ctx, m, 0, min(cur, n-1), Forward); ok {
			return pos, true, true
		}
		return engine.SourcePosition{}, false, false
	}
	if pos, ok := s.scanRows(ctx, m, 0, last, Backward); ok {
		return pos, true, false
	}
	if pos, ok := s.scanRows(ctx, m, cur, n-1, Backward); ok {
		return pos, true, true
	}
	return engine.SourcePosition{}, false, false
}
