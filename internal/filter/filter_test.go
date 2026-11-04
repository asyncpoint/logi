package filter

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

func rec(sev engine.Severity, msg string) *engine.LogRecord {
	return &engine.LogRecord{Severity: sev, Message: msg, Raw: engine.RawRecord{Data: msg}, SourceID: "app.log"}
}

func TestFilterTerms(t *testing.T) {
	json := &engine.LogRecord{Severity: engine.SevInfo, Message: "done", Raw: engine.RawRecord{Data: `{"msg":"done","status":"ok","n":5}`}}
	nc := &engine.LogRecord{Severity: engine.SevError, Message: "x", Fields: engine.Fields{{Key: "thread", Value: "T1"}}, Raw: engine.RawRecord{Data: "x"}}
	cases := []struct {
		q    string
		r    *engine.LogRecord
		want bool
	}{
		{"severity:error", rec(engine.SevError, "a"), true},
		{"severity:error", rec(engine.SevWarn, "a"), false},
		{"severity:>=warn", rec(engine.SevFatal, "a"), true},
		{"severity:>=warn", rec(engine.SevInfo, "a"), false},
		{"level:warn,error", rec(engine.SevError, "a"), true},
		{"severity:<info", rec(engine.SevDebug, "a"), true},
		{"severity:<info", rec(engine.SevUnknown, "a"), false},
		{`message:"Connection Failed"`, rec(0, "the connection failed again"), true},
		{"connection retry", rec(0, "Connection lost, retry 2"), true},
		{"connection retry", rec(0, "Connection lost"), false},
		{"-debug severity:info", rec(engine.SevInfo, "hello"), true},
		{"-hello", rec(engine.SevInfo, "hello"), false},
		{"source:APP", rec(0, "a"), true},
		{"source:other", rec(0, "a"), false},
		{`re:timeout\s+\d+`, rec(0, "timeout   30s"), true},
		{"status=ok", json, true},
		{"status=bad", json, false},
		{"thread=t1", nc, true},
		{"", rec(0, "a"), true},
	}
	for _, c := range cases {
		p, err := Compile(c.q)
		if err != nil {
			t.Fatalf("%q: %v", c.q, err)
		}
		got := p == nil || p.Match(c.r)
		if got != c.want {
			t.Errorf("%q on %q = %v, want %v", c.q, c.r.Message, got, c.want)
		}
	}
}

func TestFilterErrors(t *testing.T) {
	for _, q := range []string{"severity:loud", `message:"open`, "re:("} {
		if _, err := Compile(q); err == nil {
			t.Errorf("expected error for %q", q)
		}
	}
}

func TestContainsFoldNoAlloc(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { ContainsFold("Hello WORLD, Connection Failed", "connection failed") }); n != 0 {
		t.Fatalf("allocs=%v", n)
	}
	if ContainsFold("abc", "abd") || !ContainsFold("ABC", "b") || !ContainsFold("x", "") {
		t.Fatal("ContainsFold semantics")
	}
}

func TestEqualFoldNoAlloc(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { EqualFold("ERROR", "error") }); n != 0 {
		t.Fatalf("allocs=%v", n)
	}
	if !EqualFold("ERROR", "error") || EqualFold("Error", "warn") || !EqualFold("K", "k") {
		t.Fatal("EqualFold semantics")
	}
}

func naiveContainsFold(s, lower string) bool {
	return strings.Contains(strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}, s), lower)
}

func TestContainsFoldMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	alpha := "abAB zZ_-/:1"
	gen := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alpha[r.Intn(len(alpha))]
		}
		return string(b)
	}
	for i := 0; i < 200000; i++ {
		s := gen(r.Intn(40))
		needle := strings.ToLower(gen(r.Intn(6)))
		if got, want := ContainsFold(s, needle), naiveContainsFold(s, needle); got != want {
			t.Fatalf("ContainsFold(%q,%q)=%v want %v", s, needle, got, want)
		}
	}
}

func BenchmarkContainsFoldMiss(b *testing.B) {
	s := strings.Repeat("2026-10-15 10:00:00 INFO request handled in 12ms path=/api/v1/items ", 2)
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		ContainsFold(s, "zzz-no-such-thing")
	}
}
