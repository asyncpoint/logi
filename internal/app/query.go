package app

import (
	"context"
	"sort"
	"sync"

	"github.com/asyncpoint/logi/internal/filter"
	"github.com/asyncpoint/logi/internal/search"
)

// searchState owns the lifecycle of the current query independently from the
// source/session lifecycle. Starting a query atomically supersedes its
// predecessor and returns a generation token for rejecting stale results.
type searchState struct {
	mu      sync.Mutex
	gen     uint64
	cancel  context.CancelFunc
	matcher *search.Matcher
}

func (q *searchState) Start(parent context.Context, matcher *search.Matcher) (context.Context, uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cancel != nil {
		q.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	q.cancel = cancel
	q.matcher = matcher
	q.gen++
	return ctx, q.gen
}

func (q *searchState) Cancel() {
	q.mu.Lock()
	if q.cancel != nil {
		q.cancel()
		q.cancel = nil
	}
	q.mu.Unlock()
}

func (q *searchState) Matcher() *search.Matcher {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.matcher
}

func (q *searchState) Request() search.Request {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.matcher == nil {
		return search.Request{}
	}
	return q.matcher.Request()
}

// filteredView owns the state backing the current row view. Session coordinates
// the worker and source lifecycle; this value groups the mutable filter index
// and its synchronization so those concerns are no longer loose Session fields.
type filteredView struct {
	mu         sync.RWMutex
	query      string
	predicate  filter.Predicate
	generation uint64
	ids        []uint64 // static: dense slice; live: circular buffer
	head       int      // live circular-buffer head
	count      int      // live circular-buffer length
	processed  uint64
	building   bool
	capped     bool
	live       bool
	wake       chan struct{}
}

// begin replaces the current filter and invalidates any batch produced by an
// earlier generation. The worker remains owned by Session, not by the view.
func (v *filteredView) begin(query string, pred filter.Predicate, live bool) uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.generation++
	v.query, v.predicate = query, pred
	v.ids = nil
	v.head, v.count = 0, 0
	v.processed = 0
	v.capped = false
	v.live = live
	v.building = pred != nil
	return v.generation
}

func (v *filteredView) cursor() (filter.Predicate, uint64, uint64) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.capped && !v.live {
		return nil, v.generation, v.processed + 1
	}
	return v.predicate, v.generation, v.processed + 1
}

// commit accepts a batch only if it belongs to the current query generation.
// Static views use a dense ID slice and stop at the configured cap. Live views
// use a circular ID buffer, so maintaining a capped sliding window is O(batch)
// rather than shifting the entire match list after every batch.
func (v *filteredView) commit(gen uint64, matches []uint64, processed, oldest uint64, maxMatches int) (accepted, capped bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.generation != gen {
		return false, v.capped
	}
	if v.live {
		for _, id := range matches {
			v.appendLiveLocked(id, maxMatches)
		}
	} else {
		room := maxMatches - len(v.ids)
		if room < len(matches) {
			if room < 0 {
				room = 0
			}
			matches = matches[:room]
			v.capped = true
		}
		v.ids = append(v.ids, matches...)
		// Reaching the configured bound stops future static scans. We cannot
		// know whether more matches exist without continuing through the file.
		if maxMatches > 0 && len(v.ids) >= maxMatches {
			v.capped = true
		}
	}
	v.processed = processed
	v.trimLocked(oldest)
	if v.capped && !v.live {
		v.building = false
	}
	return true, v.capped
}

func (v *filteredView) appendLiveLocked(id uint64, maxMatches int) {
	if maxMatches <= 0 {
		return
	}
	if v.count == maxMatches {
		v.ids[v.head] = id
		v.head = (v.head + 1) % len(v.ids)
		v.capped = true
		return
	}
	if v.count == len(v.ids) {
		newCap := 1
		if len(v.ids) > 0 {
			newCap = len(v.ids) * 2
		}
		if newCap > maxMatches {
			newCap = maxMatches
		}
		next := make([]uint64, newCap)
		for i := 0; i < v.count; i++ {
			next[i] = v.idAtLocked(i)
		}
		v.ids, v.head = next, 0
	}
	v.ids[(v.head+v.count)%len(v.ids)] = id
	v.count++
}

func (v *filteredView) finish(gen uint64, indexDone bool) {
	v.mu.Lock()
	if v.generation == gen {
		v.building = !indexDone
	}
	v.mu.Unlock()
}

func (v *filteredView) trim(oldest uint64) {
	v.mu.Lock()
	v.trimLocked(oldest)
	v.mu.Unlock()
}

func (v *filteredView) trimLocked(oldest uint64) {
	if v.live {
		if oldest == 0 {
			v.ids = nil
			v.head, v.count = 0, 0
			return
		}
		for v.count > 0 && v.ids[v.head] < oldest {
			v.head = (v.head + 1) % len(v.ids)
			v.count--
		}
		if v.count == 0 {
			v.head = 0
			if len(v.ids) > 1024 {
				v.ids = nil
			}
		}
		return
	}
	if oldest == 0 || len(v.ids) == 0 || v.ids[0] >= oldest {
		return
	}
	cut := sort.Search(len(v.ids), func(i int) bool { return v.ids[i] >= oldest })
	if cut > 1024 && cut > len(v.ids)/4 {
		v.ids = append([]uint64(nil), v.ids[cut:]...)
	} else {
		v.ids = v.ids[cut:]
	}
}

func (v *filteredView) filterStatus() FilterStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return FilterStatus{Query: v.query, Building: v.predicate != nil && v.building, Matches: v.matchCountLocked(), Capped: v.capped}
}

func (v *filteredView) status() (query string, active, building, capped bool, matches int) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.query, v.predicate != nil, v.building, v.capped, v.matchCountLocked()
}

func (v *filteredView) matchCountLocked() int {
	if v.live {
		return v.count
	}
	return len(v.ids)
}

func (v *filteredView) idAtLocked(row int) uint64 {
	if v.live {
		return v.ids[(v.head+row)%len(v.ids)]
	}
	return v.ids[row]
}

// idAt returns a row's compact record ID when filtering is active. SourcePosition
// is materialized only when a caller explicitly requests a stable locator.
func (v *filteredView) idAt(row int) (uint64, bool, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.predicate == nil {
		return 0, false, false
	}
	if row < 0 || row >= v.matchCountLocked() {
		return 0, false, true
	}
	return v.idAtLocked(row), true, true
}

func (v *filteredView) rowOf(id uint64) (int, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.predicate == nil {
		return 0, false
	}
	n := v.matchCountLocked()
	if n == 0 {
		return -1, true
	}
	r := sort.Search(n, func(i int) bool { return v.idAtLocked(i) >= id })
	if r >= n {
		r = n - 1
	}
	return r, true
}

func (v *filteredView) rowCount(unfiltered int) int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.predicate != nil {
		return v.matchCountLocked()
	}
	return unfiltered
}

func (v *filteredView) active() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.predicate != nil
}
