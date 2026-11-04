package engine

import "context"

// RecordStore abstracts where records live. IDs are monotonically
// increasing and contiguous within [OldestID, NewestID].
type RecordStore interface {
	Get(id uint64) (LogRecord, bool)
	Len() int
	OldestID() uint64 // 0 when empty
	NewestID() uint64 // 0 when empty
	// Range visits records with from<=ID<=to ascending until fn returns false.
	// to==0 means "through the newest record".
	Range(from, to uint64, fn func(*LogRecord) bool)
	// Raw returns the original text of a record (lazy for static stores).
	Raw(r *LogRecord) (string, error)
}

// MatchRanger is implemented by stores that can evaluate a predicate while
// scanning, ahead of delivering records to the caller.
//
// keep (optional) is a necessary condition on a record's raw text: a record it
// rejects can never match, so the store may skip parsing it entirely. raw is
// only valid for the duration of the call. accept (optional) is the full
// predicate on the parsed record. A store may call keep and accept from several
// goroutines at once, so both must be safe for concurrent use; fn is always
// called sequentially, in ID order, from the caller's goroutine, and only for
// records that pass both. Records keep their true IDs.
//
// ctx is checked while scanning, including across records that are rejected
// without ever reaching fn, so a cancelled scan stops promptly.
type MatchRanger interface {
	RangeMatching(ctx context.Context, from, to uint64, keep func(raw string) bool, accept func(*LogRecord) bool, fn func(*LogRecord) bool)
}
