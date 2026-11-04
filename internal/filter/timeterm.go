package filter

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
)

// Options tunes how a query is compiled.
type Options struct {
	// Anchor returns the wall-clock reading (see engine.Timestamp.WallNano) that
	// relative times such as "since:15m" are measured back from: normally the
	// timestamp of the newest record, so "the last 15 minutes of the log" works
	// on old files and on logs without a time zone. It is called at most once per
	// compiled term, on first use. nil, or ok=false, means "now" (local clock).
	Anchor func() (wallNano int64, ok bool)
}

type specKind int

const (
	specRelative  specKind = iota // a duration back from the anchor
	specTimeOfDay                 // a clock time on the anchor's date
	specWall                      // a date/time without a zone: compared with wall readings
	specInstant                   // a date/time with a zone: compared with instants
)

// TimeSpec is a parsed --since/--until value.
type TimeSpec struct {
	kind specKind
	rel  time.Duration
	tod  time.Duration
	ns   int64
}

const timeHelp = "use a duration like 15m, 2h, 1d or 1h30m, or a time like 14:30, 2026-10-15, 2026-10-15 14:30 or 2026-10-15T14:30:00Z"

var (
	zonedLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05Z07:00"}
	wallLayouts  = []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04"}
	todLayouts   = []string{"15:04:05.999999999", "15:04"}
)

// ParseTimeSpec parses a time window bound. until selects the end of the day for
// a bare date ("--until 2026-10-15" includes that whole day).
func ParseTimeSpec(s string, until bool) (TimeSpec, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return TimeSpec{}, fmt.Errorf("empty time: %s", timeHelp)
	}
	if d, ok := parseRelative(v); ok {
		return TimeSpec{kind: specRelative, rel: d}, nil
	}
	for _, l := range zonedLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return TimeSpec{kind: specInstant, ns: t.UnixNano()}, nil
		}
	}
	for _, l := range wallLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return TimeSpec{kind: specWall, ns: t.UnixNano()}, nil
		}
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		ns := t.UnixNano()
		if until {
			ns += int64(24*time.Hour) - 1
		}
		return TimeSpec{kind: specWall, ns: ns}, nil
	}
	for _, l := range todLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return TimeSpec{kind: specTimeOfDay, tod: time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute +
				time.Duration(t.Second())*time.Second + time.Duration(t.Nanosecond())}, nil
		}
	}
	return TimeSpec{}, fmt.Errorf("invalid time %q: %s", s, timeHelp)
}

// parseRelative reads durations made of whole numbers and the units w, d, h, m, s.
func parseRelative(s string) (time.Duration, bool) {
	var total time.Duration
	i, parts := 0, 0
	for i < len(s) {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i || j == len(s) || j-i > 9 {
			return 0, false
		}
		n := time.Duration(0)
		for _, c := range s[i:j] {
			n = n*10 + time.Duration(c-'0')
		}
		switch s[j] {
		case 'w':
			total += n * 7 * 24 * time.Hour
		case 'd':
			total += n * 24 * time.Hour
		case 'h':
			total += n * time.Hour
		case 'm':
			total += n * time.Minute
		case 's':
			total += n * time.Second
		default:
			return 0, false
		}
		parts++
		i = j + 1
	}
	return total, parts > 0 && total > 0
}

// timePred is "since:" / "until:". Records without a timestamp are kept: a bound
// cannot be judged for them, and hiding data the parser could not date would be
// worse than showing it.
type timePred struct {
	spec   TimeSpec
	until  bool
	anchor func() (int64, bool)
	once   sync.Once
	bound  int64
}

func (p *timePred) resolve() {
	switch p.spec.kind {
	case specWall, specInstant:
		p.bound = p.spec.ns
		return
	}
	a, ok := int64(0), false
	if p.anchor != nil {
		a, ok = p.anchor()
	}
	if !ok {
		n := time.Now()
		_, off := n.Zone()
		a = n.UnixNano() + int64(off)*1e9
	}
	if p.spec.kind == specRelative {
		p.bound = a - int64(p.spec.rel)
		return
	}
	day := time.Unix(0, a).UTC().Truncate(24 * time.Hour)
	p.bound = day.UnixNano() + int64(p.spec.tod)
}

func (p *timePred) Match(r *engine.LogRecord) bool {
	if r.Timestamp.IsZero() {
		return true
	}
	p.once.Do(p.resolve)
	v := r.Timestamp.WallNano()
	if p.spec.kind == specInstant {
		v = r.Timestamp.UnixNano()
	}
	if p.until {
		return v <= p.bound
	}
	return v >= p.bound
}

func compileTime(val string, until bool, o Options) (Predicate, error) {
	spec, err := ParseTimeSpec(val, until)
	if err != nil {
		key := "since"
		if until {
			key = "until"
		}
		return nil, fmt.Errorf("%s: %v", key, err)
	}
	return &timePred{spec: spec, until: until, anchor: o.Anchor}, nil
}
