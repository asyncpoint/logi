package engine

import (
	"strings"
	"testing"
)

func rec(sev Severity, txt string) LogRecord {
	return LogRecord{Severity: sev, Message: txt, Raw: RawRecord{Data: txt}}
}

func TestRingEvictsOldestAndKeepsStatsExact(t *testing.T) {
	st := &Stats{}
	r := NewRingStore(3, 0, st)
	var evicted []uint64
	r.SetEvictionHandler(func(x *LogRecord) { evicted = append(evicted, x.ID) })
	for i := 0; i < 5; i++ {
		sev := SevInfo
		if i%2 == 0 {
			sev = SevError
		}
		r.Append(rec(sev, "x"))
	}
	if r.Len() != 3 || r.OldestID() != 3 || r.NewestID() != 5 {
		t.Fatalf("len=%d oldest=%d newest=%d", r.Len(), r.OldestID(), r.NewestID())
	}
	if len(evicted) != 2 || evicted[0] != 1 || evicted[1] != 2 {
		t.Fatalf("evicted %v", evicted)
	}
	s := st.Snapshot()
	// remaining IDs 3(error),4(info),5(error)
	if s.Total != 3 || s.BySeverity[SevError] != 2 || s.BySeverity[SevInfo] != 1 || s.Evicted != 2 || s.Ingested != 5 {
		t.Fatalf("stats %+v", s)
	}
	if s.Bytes != r.Bytes() {
		t.Fatalf("stats bytes=%d ring bytes=%d", s.Bytes, r.Bytes())
	}
	if _, ok := r.Get(2); ok {
		t.Fatal("evicted record must be gone")
	}
	if g, ok := r.Get(4); !ok || g.ID != 4 {
		t.Fatal("Get(4)")
	}
}

func TestRingByteBudget(t *testing.T) {
	r := NewRingStore(1000, 1000, nil)
	big := rec(SevInfo, string(make([]byte, 300)))
	for i := 0; i < 20; i++ {
		r.Append(big)
	}
	if r.Bytes() > 1000 || r.Len() < 1 || r.Len() > 3 {
		t.Fatalf("byte budget violated: bytes=%d len=%d", r.Bytes(), r.Len())
	}
	if r.NewestID() != 20 {
		t.Fatal("newest must always survive")
	}
}

func TestRingRowMappingUsesOneConsistentSnapshot(t *testing.T) {
	r := NewRingStore(3, 0, nil)
	for _, msg := range []string{"a", "b", "c", "d"} {
		r.Append(rec(SevInfo, msg))
	}
	if id, ok := r.IDAtRow(0); !ok || id != 2 {
		t.Fatalf("IDAtRow(0)=(%d,%v), want (2,true)", id, ok)
	}
	if id, ok := r.IDAtRow(2); !ok || id != 4 {
		t.Fatalf("IDAtRow(2)=(%d,%v), want (4,true)", id, ok)
	}
	if _, ok := r.IDAtRow(3); ok {
		t.Fatal("IDAtRow past end should fail")
	}
	for id, want := range map[uint64]int{1: 0, 2: 0, 3: 1, 4: 2, 99: 2} {
		if got := r.RowOf(id); got != want {
			t.Errorf("RowOf(%d)=%d, want %d", id, got, want)
		}
	}
	r.Clear()
	if got := r.RowOf(4); got != -1 {
		t.Fatalf("RowOf on empty ring=%d, want -1", got)
	}
}

func TestRingRangeAndBatch(t *testing.T) {
	r := NewRingStore(4, 0, nil)
	batch := []LogRecord{rec(0, "a"), rec(0, "b"), rec(0, "c"), rec(0, "d"), rec(0, "e"), rec(0, "f")}
	first, last, ev := r.AppendBatch(batch)
	if first != 1 || last != 6 || ev != 2 {
		t.Fatalf("%d %d %d", first, last, ev)
	}
	var got string
	r.Range(0, 0, func(x *LogRecord) bool { got += x.Message; return true })
	if got != "cdef" {
		t.Fatalf("range %q", got)
	}
	got = ""
	r.Range(4, 5, func(x *LogRecord) bool { got += x.Message; return true })
	if got != "de" {
		t.Fatalf("subrange %q", got)
	}
}

func TestClearKeepsIDsIncreasing(t *testing.T) {
	st := &Stats{}
	r := NewRingStore(3, 0, st)
	r.Append(rec(SevInfo, "a"))
	r.Clear()
	if r.Len() != 0 || st.Snapshot().Total != 0 {
		t.Fatal("clear")
	}
	if id := r.Append(rec(SevInfo, "b")); id != 2 {
		t.Fatalf("id after clear = %d", id)
	}
}

func TestParseSeverity(t *testing.T) {
	for in, want := range map[string]Severity{"info": SevInfo, "WARNING": SevWarn, "Err": SevError, "fatal": SevFatal, "nope": SevUnknown, "": SevUnknown} {
		if ParseSeverity(in) != want {
			t.Errorf("%q", in)
		}
	}
}

func TestLogRecordRetainedBytesIncludesStructuredOverhead(t *testing.T) {
	raw := strings.Repeat("x", 256)
	r := LogRecord{
		Message:  raw,
		Raw:      RawRecord{Data: raw},
		SourceID: "app.log",
		Fields:   Fields{{Key: "thread", Value: "worker-1"}, {Key: "event", Value: "Cache.Get"}},
	}
	base := LogRecord{Message: raw, Raw: RawRecord{Data: raw}, SourceID: "app.log"}
	if got := r.RetainedBytes(); got <= base.RetainedBytes() {
		t.Fatalf("structured fields were not reflected in retained size: base=%d fields=%d", base.RetainedBytes(), got)
	}
	if got := r.RetainedBytes(); got < len(raw) {
		t.Fatalf("retained size %d is smaller than raw payload %d", got, len(raw))
	}
}

func BenchmarkRingAppend(b *testing.B) {
	r := NewRingStore(50000, 0, &Stats{})
	x := rec(SevInfo, "hello world")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Append(x)
	}
}

func TestEveryKnownLevelSpelling(t *testing.T) {
	want := map[Severity][]string{
		SevTrace:    {"TRACE", "trc", "Trce", "TRAC", "verbose", "VRB", "FINEST", "finer"},
		SevDebug:    {"DEBUG", "dbg", "Dbug", "DEBU", "FINE", "config", "DEB"},
		SevInfo:     {"INFO", "inf", "Information", "informational"},
		SevNotice:   {"NOTICE", "note", "SUCCESS"},
		SevWarn:     {"WARN", "Warning", "wrn"},
		SevError:    {"ERROR", "err", "ERRO", "SEVERE", "fail", "Exception"},
		SevCritical: {"CRITICAL", "crit", "ALERT", "DPANIC"},
		SevFatal:    {"FATAL", "fata", "FTL", "PANIC", "emerg", "Emergency"},
	}
	for sev, names := range want {
		for _, n := range names {
			if got := ParseSeverity(n); got != sev {
				t.Errorf("ParseSeverity(%q) = %v, want %v", n, got, sev)
			}
		}
	}
	for _, n := range []string{"", "x", "in", "hello", "INFOX", "informationalx", "ok"} {
		if ParseSeverity(n) != SevUnknown {
			t.Errorf("%q must be unknown", n)
		}
	}
	for c, sev := range map[byte]Severity{'V': SevTrace, 'D': SevDebug, 'i': SevInfo, 'N': SevNotice, 'W': SevWarn, 'E': SevError, 'C': SevCritical, 'F': SevFatal} {
		if ParseSeverityLetter(c) != sev {
			t.Errorf("letter %c", c)
		}
	}
	if ParseSeverityLetter('x') != SevUnknown || len(AllSeverities()) != NumSeverities-1 {
		t.Fatal("letters / AllSeverities")
	}
	if n := testing.AllocsPerRun(100, func() { ParseSeverity("Warning") }); n != 0 {
		t.Fatalf("allocs=%v", n)
	}
}

func TestWindowsEventLogLevels(t *testing.T) {
	for in, want := range map[string]Severity{"Information": SevInfo, "Warning": SevWarn, "Error": SevError, "Critical": SevCritical,
		"Verbose": SevTrace, "SuccessAudit": SevNotice, "FailureAudit": SevError, "AuditFailure": SevError} {
		if got := ParseSeverity(in); got != want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLogRecordDetachRawReleasesBackingPayload(t *testing.T) {
	raw := `2026-09-20 06:33:57.301 INFO this is a large raw payload with fields`
	r := LogRecord{
		Message: raw[28:],
		Fields:  Fields{{Key: "component", Value: raw[0:10]}, {Key: "payload", Value: raw[10:], Raw: true}},
		Raw:     RawRecord{Data: raw, Offset: 10, Length: int64(len(raw))},
	}
	before := r.RetainedBytes()
	r.DetachRaw()
	if r.Raw.Data != "" {
		t.Fatal("DetachRaw retained Raw.Data")
	}
	if r.Message == "" || r.Fields.GetValue("component") == "" || r.Fields.GetValue("payload") == "" {
		t.Fatalf("detached semantic data was lost: %+v", r)
	}
	if got := r.RetainedBytes(); got <= 0 || got >= before {
		t.Fatalf("detach did not reduce retained size: before=%d after=%d", before, got)
	}
}

func TestSourcePositionLiveResolutionAndEviction(t *testing.T) {
	ring := NewRingStore(2, 0, nil)
	first := ring.Append(LogRecord{SourceID: "live.log", Raw: RawRecord{Data: "first"}})
	pos, ok := ring.Position("live.log", first)
	if !ok || pos.Kind != PositionLiveID || pos.RecordID != first || pos.Offset != 0 || pos.Length != 0 {
		t.Fatalf("unexpected live position: %#v, ok=%v", pos, ok)
	}
	if _, ok := ring.ResolvePosition("other.log", pos); ok {
		t.Fatal("position resolved against a different source")
	}
	if rec, ok := ring.ResolvePosition("live.log", pos); !ok || rec.Raw.Data != "first" {
		t.Fatalf("position failed to resolve before eviction: %#v, %v", rec, ok)
	}
	ring.Append(LogRecord{Raw: RawRecord{Data: "second"}})
	ring.Append(LogRecord{Raw: RawRecord{Data: "third"}})
	if _, ok := ring.ResolvePosition("live.log", pos); ok {
		t.Fatal("evicted live position should be stale")
	}
}

func TestSourcePositionHasNoPayload(t *testing.T) {
	p := SourcePosition{SourceID: "app.log", Kind: PositionStaticOffset, RecordID: 42, Offset: 4096, Length: 120}
	if p.IsZero() {
		t.Fatal("valid source position reported as zero")
	}
	if p.SourceID != "app.log" || p.Offset != 4096 || p.Length != 120 || p.RecordID != 42 {
		t.Fatalf("position locator fields changed: %#v", p)
	}
	if (SourcePosition{}).IsZero() == false {
		t.Fatal("zero position should be invalid")
	}
}
