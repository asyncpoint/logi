package engine

import (
	"sync"
	"time"
)

// Timestamp is a compact, pointer-free wall-clock time (16 bytes versus 24 for
// time.Time, which also carries a *Location the garbage collector must trace for
// every retained record). It keeps nanosecond precision and the UTC offset the
// log was written in. The zero value means "no timestamp".
//
// Range is that of UnixNano (years 1678–2262); values outside are clamped.
type Timestamp struct {
	ns  int64
	off int32 // UTC offset in seconds
	set bool
}

const maxSec int64 = 9223372035 // sec*1e9 + nanoseconds stays inside int64

// TimestampOf converts t; the zero time.Time yields the zero Timestamp.
func TimestampOf(t time.Time) Timestamp {
	if t.IsZero() {
		return Timestamp{}
	}
	_, off := t.Zone()
	sec := t.Unix()
	switch {
	case sec > maxSec:
		return Timestamp{ns: 1<<63 - 1, off: int32(off), set: true}
	case sec < -maxSec:
		return Timestamp{ns: -1 << 63, off: int32(off), set: true}
	}
	return Timestamp{ns: sec*1e9 + int64(t.Nanosecond()), off: int32(off), set: true}
}

var zones sync.Map // int32 offset -> *time.Location

func zoneFor(off int32) *time.Location {
	if off == 0 {
		return time.UTC
	}
	if v, ok := zones.Load(off); ok {
		return v.(*time.Location)
	}
	v, _ := zones.LoadOrStore(off, time.FixedZone("", int(off)))
	return v.(*time.Location)
}

// IsZero reports whether no timestamp was present.
func (t Timestamp) IsZero() bool { return !t.set }

// Time returns the instant in the zone it was logged in (zero time if unset).
func (t Timestamp) Time() time.Time {
	if !t.set {
		return time.Time{}
	}
	return time.Unix(0, t.ns).In(zoneFor(t.off))
}

// WallNano is the wall-clock reading as nanoseconds, as if it were UTC: the
// instant plus the UTC offset the log was written in. Logs without a time zone
// are stored as UTC readings, so comparing wall readings compares what a person
// reads in the log, whatever zone it was written in (0 if unset).
func (t Timestamp) WallNano() int64 {
	if !t.set {
		return 0
	}
	return t.ns + int64(t.off)*1e9
}

// UnixNano returns nanoseconds since the Unix epoch (0 if unset).
func (t Timestamp) UnixNano() int64 { return t.ns }

func (t Timestamp) Format(layout string) string { return t.Time().Format(layout) }
func (t Timestamp) String() string {
	if !t.set {
		return "<no timestamp>"
	}
	return t.Time().String()
}
func (t Timestamp) Year() int               { return t.Time().Year() }
func (t Timestamp) Month() time.Month       { return t.Time().Month() }
func (t Timestamp) Hour() int               { return t.Time().Hour() }
func (t Timestamp) Minute() int             { return t.Time().Minute() }
func (t Timestamp) Second() int             { return t.Time().Second() }
func (t Timestamp) Before(u Timestamp) bool { return t.ns < u.ns }
func (t Timestamp) After(u Timestamp) bool  { return t.ns > u.ns }
