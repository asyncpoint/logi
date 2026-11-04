package engine

import (
	"testing"
	"time"
	"unsafe"
)

func TestTimestampRoundTrip(t *testing.T) {
	if !(Timestamp{}).IsZero() || !TimestampOf(time.Time{}).IsZero() || !(Timestamp{}).Time().IsZero() {
		t.Fatal("zero value must mean no timestamp")
	}
	ist := time.FixedZone("", 5*3600+1800)
	for _, in := range []time.Time{
		time.Date(2026, 10, 15, 10, 0, 0, 123456789, time.UTC),
		time.Date(2026, 10, 15, 10, 0, 0, 5, ist),
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), // epoch is a real timestamp, not "unset"
		time.Date(1999, 12, 31, 23, 59, 59, 999, time.FixedZone("", -3*3600)),
	} {
		out := TimestampOf(in).Time()
		if !out.Equal(in) {
			t.Errorf("%v round-tripped to %v", in, out)
		}
		if _, a := in.Zone(); func() int { _, b := out.Zone(); return b }() != a {
			t.Errorf("zone offset lost for %v", in)
		}
		if TimestampOf(in).Format(time.RFC3339Nano) != in.Format(time.RFC3339Nano) {
			t.Errorf("format differs for %v", in)
		}
	}
	if TimestampOf(time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() || TimestampOf(time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC)).IsZero() {
		t.Error("out-of-range values must clamp, not become unset")
	}
}

func TestTimestampIsCompact(t *testing.T) {
	if s := unsafe.Sizeof(Timestamp{}); s != 16 {
		t.Errorf("sizeof(Timestamp) = %d, want 16", s)
	}
	if testing.AllocsPerRun(100, func() { _ = TimestampOf(time.Now()).Time() }) > 0 {
		t.Error("conversion allocates")
	}
}
