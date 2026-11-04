package app

import (
	"sync/atomic"

	"github.com/asyncpoint/logi/internal/engine"
)

// censusChunk is the least number of newly indexed records worth a scan of their
// own while the file is still being indexed; the remainder is scanned at the end.
const censusChunk = 1 << 16

// severityCensus counts the records of a static file by severity, so the status
// bar can show how many errors and warnings the file holds, as it does for a live
// log. Static mode keeps no records in memory, so the counts come from a
// background scan that retains nothing.
type severityCensus struct {
	counts [engine.NumSeverities]atomic.Int64
	done   atomic.Bool
}

// add counts one record. It is called from several scan workers at once and
// always returns false: the scan only looks at records, it never collects them.
func (c *severityCensus) add(r *engine.LogRecord) bool {
	c.counts[r.Severity].Add(1)
	return false
}

// snapshot returns the counts so far and whether the scan has covered the file.
func (c *severityCensus) snapshot() (by [engine.NumSeverities]int64, done bool) {
	done = c.done.Load()
	for i := range by {
		by[i] = c.counts[i].Load()
	}
	return by, done
}

// censusWorker follows the index: it scans each newly indexed stretch of the file
// once, in parallel, and finishes when the whole file has been indexed and
// scanned. A scan interrupted by Close simply stops; the counts stay partial.
func (s *Session) censusWorker() {
	var scanned uint64
	for s.ctx.Err() == nil {
		done := s.indexDone() // read before the length: once done, the length is final
		newest := s.static.NewestID()
		if newest > scanned && (done || newest-scanned >= censusChunk) {
			s.static.RangeMatching(s.ctx, scanned+1, newest, nil, s.census.add, func(*engine.LogRecord) bool { return true })
			if s.ctx.Err() != nil {
				return
			}
			scanned = newest
			s.em.with(func(u *Update) { u.Counts = true })
			continue
		}
		if done && newest == scanned {
			s.census.done.Store(true)
			s.em.with(func(u *Update) { u.Counts = true })
			return
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.censusWake:
		}
	}
}
