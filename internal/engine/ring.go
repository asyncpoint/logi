package engine

import "sync"

// RingStore is the bounded live store. Memory is bounded by a record
// capacity and an approximate byte budget, so RAM ≈ configuration rather than
// duration × log rate.
type RingStore struct {
	mu       sync.RWMutex
	buf      []LogRecord
	head     int    // index of the oldest record in buf
	n        int    // live records
	oldestID uint64 // ID of buf[head]; valid when n>0
	nextID   uint64
	bytes    int64
	maxBytes int64
	stats    *Stats
	onEvict  func(*LogRecord) // called under lock: must be cheap
}

// NewRingStore creates a ring with the given record capacity and byte budget
// (maxBytes<=0 disables the byte budget).
func NewRingStore(capacity int, maxBytes int64, stats *Stats) *RingStore {
	if capacity < 1 {
		capacity = 1
	}
	return &RingStore{buf: make([]LogRecord, capacity), nextID: 1, maxBytes: maxBytes, stats: stats}
}

// SetEvictionHandler registers a cheap callback invoked for every eviction.
func (r *RingStore) SetEvictionHandler(fn func(*LogRecord)) { r.onEvict = fn }

// Capacity returns the configured record capacity.
func (r *RingStore) Capacity() int { return len(r.buf) }

func (r *RingStore) evictOldestLocked() {
	rec := &r.buf[r.head]
	sz := int64(rec.RetainedBytes())
	r.bytes -= sz
	if r.stats != nil {
		r.stats.removeRetained(rec, sz)
	}
	if r.onEvict != nil {
		r.onEvict(rec)
	}
	*rec = LogRecord{} // release strings for the GC
	r.head = (r.head + 1) % len(r.buf)
	r.n--
	r.oldestID++
}

// AppendBatch stores records, assigning IDs in place. It returns the first and
// last assigned ID and the number of evicted records.
func (r *RingStore) AppendBatch(recs []LogRecord) (first, last uint64, evicted int) {
	if len(recs) == 0 {
		return 0, 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	first = r.nextID
	for i := range recs {
		rec := &recs[i]
		rec.ID = r.nextID
		r.nextID++
		sz := int64(rec.RetainedBytes())
		for r.n > 0 && (r.n == len(r.buf) || (r.maxBytes > 0 && r.bytes+sz > r.maxBytes)) {
			r.evictOldestLocked()
			evicted++
		}
		if r.n == 0 {
			r.head = 0
			r.oldestID = rec.ID
		}
		r.buf[(r.head+r.n)%len(r.buf)] = *rec
		r.n++
		r.bytes += sz
		if r.stats != nil {
			r.stats.addRetained(rec, sz)
		}
	}
	return first, r.nextID - 1, evicted
}

// Append stores one record and returns its ID.
func (r *RingStore) Append(rec LogRecord) uint64 {
	one := [1]LogRecord{rec}
	first, _, _ := r.AppendBatch(one[:])
	return first
}

// Clear drops every record (truncation policy). IDs keep increasing.
func (r *RingStore) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.n > 0 {
		r.evictOldestLocked()
	}
	r.head, r.bytes = 0, 0
}

func (r *RingStore) Get(id uint64) (LogRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.n == 0 || id < r.oldestID || id >= r.oldestID+uint64(r.n) {
		return LogRecord{}, false
	}
	return r.buf[(r.head+int(id-r.oldestID))%len(r.buf)], true
}

func (r *RingStore) Len() int { r.mu.RLock(); defer r.mu.RUnlock(); return r.n }

func (r *RingStore) OldestID() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.n == 0 {
		return 0
	}
	return r.oldestID
}

func (r *RingStore) NewestID() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.n == 0 {
		return 0
	}
	return r.oldestID + uint64(r.n) - 1
}

// IDAtRow maps a zero-based row to its record ID atomically with respect to
// append and eviction. It avoids composing Len and OldestID snapshots that can
// become inconsistent while a live source is advancing.
func (r *RingStore) IDAtRow(row int) (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if row < 0 || row >= r.n {
		return 0, false
	}
	return r.oldestID + uint64(row), true
}

// RowOf maps an ID to the nearest row in the current ring snapshot. IDs before
// the retained window clamp to row zero; newer IDs clamp to the last row.
func (r *RingStore) RowOf(id uint64) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.n == 0 {
		return -1
	}
	if id <= r.oldestID {
		return 0
	}
	off := id - r.oldestID
	if off >= uint64(r.n) {
		return r.n - 1
	}
	return int(off)
}

// Range visits records under a read lock. fn must not call back into the store.
func (r *RingStore) Range(from, to uint64, fn func(*LogRecord) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.n == 0 {
		return
	}
	if from < r.oldestID {
		from = r.oldestID
	}
	newest := r.oldestID + uint64(r.n) - 1
	if to == 0 || to > newest {
		to = newest
	}
	for id := from; id <= to; id++ {
		if !fn(&r.buf[(r.head+int(id-r.oldestID))%len(r.buf)]) {
			return
		}
	}
}

// Position returns the stable live-source locator for id. The position does
// not keep the record or its payload alive. It must be resolved again before
// use because ring eviction can make it stale.
func (r *RingStore) Position(sourceID string, id uint64) (SourcePosition, bool) {
	if _, ok := r.Get(id); !ok || sourceID == "" {
		return SourcePosition{}, false
	}
	return SourcePosition{SourceID: sourceID, Kind: PositionLiveID, RecordID: id}, true
}

// ResolvePosition resolves a live position, rejecting positions for another
// source and positions whose records have been evicted.
func (r *RingStore) ResolvePosition(sourceID string, p SourcePosition) (LogRecord, bool) {
	if p.Kind != PositionLiveID || p.SourceID == "" || p.SourceID != sourceID {
		return LogRecord{}, false
	}
	return r.Get(p.RecordID)
}

func (r *RingStore) Raw(rec *LogRecord) (string, error) { return rec.Raw.Data, nil }

// Bytes returns the approximate retained bytes.
func (r *RingStore) Bytes() int64 { r.mu.RLock(); defer r.mu.RUnlock(); return r.bytes }
