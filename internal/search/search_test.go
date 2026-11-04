package search

import (
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

func rec(s string) *engine.LogRecord {
	return &engine.LogRecord{Message: s, Raw: engine.RawRecord{Data: s}}
}

func TestLiteralCaseHandling(t *testing.T) {
	m, _ := Compile(Request{Query: "Failed"})
	if !m.Match(rec("connection FAILED")) {
		t.Fatal("default is case-insensitive")
	}
	m, _ = Compile(Request{Query: "Failed", CaseSensitive: true})
	if m.Match(rec("connection FAILED")) || !m.Match(rec("Failed!")) {
		t.Fatal("case-sensitive")
	}
	if m, _ := Compile(Request{}); m.Match(rec("x")) {
		t.Fatal("empty query matches nothing")
	}
}

func TestRegexAndFieldModes(t *testing.T) {
	m, err := Compile(Request{Query: `retry \d+`, Mode: Regex})
	if err != nil || !m.Match(rec("Retry 12")) || m.Match(rec("retry x")) {
		t.Fatal("regex")
	}
	if _, err := Compile(Request{Query: "(", Mode: Regex}); err == nil {
		t.Fatal("bad regex must error")
	}
	j := &engine.LogRecord{Raw: engine.RawRecord{Data: `{"status":"OK","n":1}`}}
	f, _ := Compile(Request{Query: "status=ok", Mode: Field})
	if !f.Match(j) {
		t.Fatal("field")
	}
	if _, err := Compile(Request{Query: "nokey", Mode: Field}); err == nil {
		t.Fatal("field needs key=value")
	}
}

func TestSpans(t *testing.T) {
	m, _ := Compile(Request{Query: "ab"})
	sp := m.Spans("xxABxxabx")
	if len(sp) != 2 || sp[0] != [2]int{2, 4} || sp[1] != [2]int{6, 8} {
		t.Fatalf("%v", sp)
	}
}

// A pathological pattern must stay linear thanks to RE2.
func TestRegexIsLinearTime(t *testing.T) {
	m, _ := Compile(Request{Query: `(a+)+$`, Mode: Regex})
	long := make([]byte, 200000)
	for i := range long {
		long[i] = 'a'
	}
	long = append(long, 'b')
	m.Match(rec(string(long))) // would hang a backtracking engine
}

func BenchmarkLiteralMatchCaseInsensitive(b *testing.B) {
	m, err := Compile(Request{Query: "connection reset", CaseSensitive: false})
	if err != nil {
		b.Fatal(err)
	}
	r := rec("2026-10-09T10:11:12Z INFO worker-7 connection reset by peer after request processing")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !m.Match(r) {
			b.Fatal("expected match")
		}
	}
}

func TestLiteralSearchIsUnicodeCaseInsensitive(t *testing.T) {
	m, err := Compile(Request{Query: "école"})
	if err != nil {
		t.Fatal(err)
	}
	rec := &engine.LogRecord{Raw: engine.RawRecord{Data: "x ÉCOLE y"}, Message: "x ÉCOLE y"}
	if !m.Match(rec) {
		t.Fatal("literal search for \"école\" must match \"ÉCOLE\"")
	}
	if keep := m.RawKeep(); keep == nil || !keep(rec.Raw.Data) {
		t.Fatal("raw prefilter must accept a record the matcher accepts")
	}
	spans := m.Spans("x ÉCOLE y école")
	if len(spans) != 2 || "x ÉCOLE y école"[spans[0][0]:spans[0][1]] != "ÉCOLE" {
		t.Fatalf("highlight spans wrong: %v", spans)
	}
	cs, _ := Compile(Request{Query: "école", CaseSensitive: true})
	if cs.Match(rec) {
		t.Fatal("case-sensitive search must not match different case")
	}
}
