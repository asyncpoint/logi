package app

// Row-view access: ID/position/record lookups for the rows the UI shows.

import (
	"github.com/asyncpoint/logi/internal/engine"
)

// ---- view ---------------------------------------------------------------

// ViewLen is the number of rows in the current view.
func (s *Session) ViewLen() int { return s.view.rowCount(s.store.Len()) }

// IDAt maps a view row to a record ID.
func (s *Session) IDAt(row int) (uint64, bool) {
	if row < 0 {
		return 0, false
	}
	if id, ok, active := s.view.idAt(row); active {
		return id, ok
	}
	if s.ring != nil {
		return s.ring.IDAtRow(row)
	}
	oldest, n := s.store.OldestID(), s.store.Len()
	if oldest == 0 || row >= n {
		return 0, false
	}
	return oldest + uint64(row), true
}

// PositionAt returns a payload-free identity for the record shown at row.
// It is suitable for future bookmark and jump-to-record features.
func (s *Session) PositionAt(row int) (engine.SourcePosition, bool) {
	if row < 0 {
		return engine.SourcePosition{}, false
	}
	id, ok := s.IDAt(row)
	if !ok {
		return engine.SourcePosition{}, false
	}
	if s.mode == ModeLive {
		rec, ok := s.ring.Get(id)
		if !ok {
			return engine.SourcePosition{}, false
		}
		sid := rec.SourceID
		if sid == "" {
			sid = sourceID(s.path)
		}
		return engine.SourcePosition{SourceID: sid, Kind: engine.PositionLiveID, RecordID: id}, true
	}
	return s.static.Position(id)
}

// ResolvePosition resolves a previously captured position for this session.
// It rejects cross-source positions and returns false for stale live records.
func (s *Session) ResolvePosition(p engine.SourcePosition) (engine.LogRecord, bool) {
	if p.IsZero() {
		return engine.LogRecord{}, false
	}
	if s.mode == ModeLive {
		if p.Kind != engine.PositionLiveID {
			return engine.LogRecord{}, false
		}
		rec, ok := s.ring.Get(p.RecordID)
		if !ok {
			return engine.LogRecord{}, false
		}
		if rec.SourceID != "" && rec.SourceID != p.SourceID {
			return engine.LogRecord{}, false
		}
		return rec, true
	}
	if p.SourceID != sourceID(s.path) {
		return engine.LogRecord{}, false
	}
	return s.static.ResolvePosition(p)
}

// RowOf returns the row of the first record whose ID is >= id, clamped to the
// view, or -1 for an empty view.
func (s *Session) RowOf(id uint64) int {
	if row, active := s.view.rowOf(id); active {
		return row
	}
	if s.ring != nil {
		return s.ring.RowOf(id)
	}
	n := s.store.Len()
	if n == 0 {
		return -1
	}
	oldest := s.store.OldestID()
	if id <= oldest {
		return 0
	}
	if r := int(id - oldest); r < n {
		return r
	}
	return n - 1
}

// ViewAt returns the record shown on a row.
func (s *Session) ViewAt(row int) (engine.LogRecord, bool) {
	id, ok := s.IDAt(row)
	if !ok {
		return engine.LogRecord{}, false
	}
	return s.store.Get(id)
}

// Record fetches a record by ID.
func (s *Session) Record(id uint64) (engine.LogRecord, bool) { return s.store.Get(id) }

// Raw returns the original text of a record.
func (s *Session) Raw(r *engine.LogRecord) (string, error) { return s.store.Raw(r) }
