// Package input reads bytes from files: a lazy offset-based static store and
// an incremental, rotation-aware tailer.
package input

// SourceState describes the health of a live source.
type SourceState int

const (
	StateRunning   SourceState = iota
	StateWaiting               // file missing; waiting for (re)creation
	StateRotated               // path now names a different file; reopened
	StateTruncated             // same file shrank; restarted from the beginning
	StateError                 // recoverable read error
	StateEnded                 // the input stream ended (standard input closed)
)

func (s SourceState) String() string {
	return [...]string{"running", "waiting", "rotated", "truncated", "error", "ended"}[s]
}

// Sink receives coarse, batched notifications. Implementations must be cheap
// and must never block: the ingestion pipeline does not wait for the UI.
type Sink interface {
	Added(first, last uint64, evicted int)
	State(s SourceState, msg string)
}
