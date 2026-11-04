package filter

import (
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
)

func timedRec(ts string) *engine.LogRecord {
	if ts == "" {
		return &engine.LogRecord{Message: "untimed"}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		panic(err)
	}
	return &engine.LogRecord{Timestamp: engine.TimestampOf(t), Message: ts}
}

// anchorAt returns an Anchor that reports the given wall-clock reading.
func anchorAt(ts string) func() (int64, bool) {
	t, _ := time.Parse("2006-01-02 15:04:05", ts)
	return func() (int64, bool) { return t.UnixNano(), true }
}

func TestParseTimeSpec(t *testing.T) {
	for _, ok := range []string{"15m", "2h", "1d", "1h30m", "90s", "2w", "14:30", "14:30:15", "2026-10-15", "2026-10-15 14:30", "2026-10-15T14:30:00", "2026-10-15T14:30:00Z", "2026-10-15T14:30:00+05:00", "2026-10-15 14:30:15.250"} {
		if _, err := ParseTimeSpec(ok, false); err != nil {
			t.Errorf("%q should parse: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "15", "15x", "m15", "0m", "1h-", "abc", "2026-13-01", "25:00", "1.5h"} {
		if _, err := ParseTimeSpec(bad, false); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestSinceUntilRelativeToTheNewestRecord(t *testing.T) {
	// Logs without a zone: the log's own clock is the reference, not the wall clock.
	o := Options{Anchor: anchorAt("2026-10-15 10:00:00")}
	since, err := CompileWith("since:15m", o)
	if err != nil {
		t.Fatal(err)
	}
	for ts, want := range map[string]bool{
		"2026-10-15T09:44:59Z": false, "2026-10-15T09:45:00Z": true, "2026-10-15T10:00:00Z": true, "2026-10-14T10:00:00Z": false,
	} {
		if got := since.Match(timedRec(ts)); got != want {
			t.Errorf("since:15m on %s = %v, want %v", ts, got, want)
		}
	}
	if !since.Match(timedRec("")) {
		t.Error("records without a timestamp are kept")
	}
	until, _ := CompileWith("until:1h", o)
	if !until.Match(timedRec("2026-10-15T08:59:59Z")) || until.Match(timedRec("2026-10-15T09:30:00Z")) {
		t.Error("until:1h keeps records up to an hour before the newest")
	}
}

func TestAbsoluteAndTimeOfDayBounds(t *testing.T) {
	o := Options{Anchor: anchorAt("2026-10-15 18:00:00")}
	cases := []struct {
		q    string
		ts   string
		want bool
	}{
		{`since:"2026-10-15 10:30"`, "2026-10-15T10:29:59Z", false},
		{`since:"2026-10-15 10:30"`, "2026-10-15T10:30:00Z", true},
		{"until:2026-10-15", "2026-10-15T23:59:59Z", true}, // a bare until date includes the whole day
		{"until:2026-10-15", "2026-10-16T00:00:00Z", false},
		{"since:2026-10-15", "2026-10-14T23:59:59Z", false},
		{"since:14:30", "2026-10-15T14:29:00Z", false}, // clock time on the anchor's date
		{"since:14:30", "2026-10-15T14:30:00Z", true},
		{"since:14:30", "2026-10-14T20:00:00Z", false},
		{"since:2026-10-15T10:00:00Z", "2026-10-15T09:59:59Z", false}, // zoned input compares instants
		{"since:2026-10-15T10:00:00+05:00", "2026-10-15T04:59:59Z", false},
		{"since:2026-10-15T10:00:00+05:00", "2026-10-15T05:00:00Z", true},
	}
	for _, c := range cases {
		p, err := CompileWith(c.q, o)
		if err != nil {
			t.Fatalf("%s: %v", c.q, err)
		}
		if got := p.Match(timedRec(c.ts)); got != c.want {
			t.Errorf("%s on %s = %v, want %v", c.q, c.ts, got, c.want)
		}
	}
}

func TestZonedLogsCompareWallClock(t *testing.T) {
	// a record logged as 10:00 in +05:00 reads as 10:00 on the page, though it is 05:00 UTC
	r := timedRec("2026-10-15T10:00:00+05:00")
	p, _ := CompileWith(`since:"2026-10-15 09:00"`, Options{})
	if !p.Match(r) {
		t.Error("a naive bound is compared with the wall reading (10:00), not the UTC instant (05:00)")
	}
}

func TestWindowCombinesAndNegates(t *testing.T) {
	o := Options{Anchor: anchorAt("2026-10-15 12:00:00")}
	p, err := CompileWith("since:1h until:10m error", o)
	if err != nil {
		t.Fatal(err)
	}
	in := timedRec("2026-10-15T11:30:00Z")
	in.Raw.Data = "an error occurred"
	if !p.Match(in) {
		t.Error("inside the window and matching text")
	}
	out := timedRec("2026-10-15T11:55:00Z")
	out.Raw.Data = "an error occurred"
	if p.Match(out) {
		t.Error("after until:10m must be excluded")
	}
	neg, _ := CompileWith("-since:1h", o)
	if !neg.Match(timedRec("2026-10-15T09:00:00Z")) || neg.Match(timedRec("2026-10-15T11:59:00Z")) {
		t.Error("negation inverts the window")
	}
	if _, err := Compile("since:soon"); err == nil {
		t.Error("an unparseable bound must be a compile error")
	}
}

func FuzzParseTimeSpec(f *testing.F) {
	for _, s := range []string{"15m", "2026-10-15T14:30:00Z", "14:30", "", "99999999999d", "1h1h1h"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ParseTimeSpec(s, false)
		ParseTimeSpec(s, true)
	})
}
