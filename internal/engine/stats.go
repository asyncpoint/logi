package engine

import "sync/atomic"

// Stats holds incrementally maintained counters. Every update is
// a single atomic operation, so statistics can never delay ingestion.
type Stats struct {
	total    atomic.Int64
	bySev    [NumSeverities]atomic.Int64
	evicted  atomic.Int64
	ingested atomic.Int64 // lifetime, never decremented
	bytes    atomic.Int64
	parseErr atomic.Int64
}

// StatsSnapshot is a copy for display.
type StatsSnapshot struct {
	Total      int64
	BySeverity [NumSeverities]int64
	Evicted    int64
	Ingested   int64
	Bytes      int64
	ParseErrs  int64
}

func (s *Stats) Add(r *LogRecord) { s.addRetained(r, int64(r.RetainedBytes())) }

// addRetained lets RingStore reuse the size estimate it already computed for
// its byte budget, avoiding a second walk over every structured field on ingest.
func (s *Stats) addRetained(r *LogRecord, retained int64) {
	s.total.Add(1)
	s.ingested.Add(1)
	s.bySev[r.Severity].Add(1)
	s.bytes.Add(retained)
	if r.ParseErr {
		s.parseErr.Add(1)
	}
}

// Remove is called when a record leaves the ring.
func (s *Stats) Remove(r *LogRecord) { s.removeRetained(r, int64(r.RetainedBytes())) }

// removeRetained reuses the ring's already-computed record size on eviction.
func (s *Stats) removeRetained(r *LogRecord, retained int64) {
	s.total.Add(-1)
	s.bySev[r.Severity].Add(-1)
	s.bytes.Add(-retained)
	s.evicted.Add(1)
}

func (s *Stats) Snapshot() StatsSnapshot {
	o := StatsSnapshot{Total: s.total.Load(), Evicted: s.evicted.Load(),
		Ingested: s.ingested.Load(), Bytes: s.bytes.Load(), ParseErrs: s.parseErr.Load()}
	for i := range s.bySev {
		o.BySeverity[i] = s.bySev[i].Load()
	}
	return o
}
