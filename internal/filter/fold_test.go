package filter

import (
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

func TestFoldMatcherUnicode(t *testing.T) {
	for _, c := range []struct {
		hay, needle string
		want        bool
	}{
		{"Bienvenue à l'ÉCOLE publique", "école", true},
		{"bienvenue à l'école", "ÉCOLE", true},
		{"ΣΑΣ", "σας", true}, // final and medial sigma are one fold class
		{"Привет МИР", "мир", true},
		{"ǅ", "ǆ", true}, // titlecase digraph
		{"hello", "héllo", false},
		{"STRASSE", "straße", false}, // simple folding is rune-to-rune; ß != SS
		{"abc", "ABC", true},
		{"ÉCOL", "école", false}, // truncated text must not match
		{"", "é", false},
	} {
		fm := NewFoldMatcher(c.needle)
		if got := fm.Contains(c.hay); got != c.want {
			t.Errorf("Contains(%q, %q) = %v, want %v", c.hay, c.needle, got, c.want)
		}
		if got := ContainsFold(c.hay, strings.ToLower(c.needle)); got != c.want {
			t.Errorf("ContainsFold(%q, %q) = %v, want %v", c.hay, c.needle, got, c.want)
		}
	}
}

func TestFoldMatcherIndexSpans(t *testing.T) {
	fm := NewFoldMatcher("école")
	s := "x ÉCOLE y école"
	a, b := fm.Index(s)
	if s[a:b] != "ÉCOLE" {
		t.Fatalf("first span = %q (%d,%d)", s[a:b], a, b)
	}
	a2, b2 := fm.Index(s[b:])
	if s[b:][a2:b2] != "école" {
		t.Fatalf("second span = %q", s[b:][a2:b2])
	}
	// case variants with different UTF-8 lengths: Kelvin sign (3 bytes) vs k (1 byte)
	fk := NewFoldMatcher("ké")
	if a, b := fk.Index("\u212aÉ"); a != 0 || b != len("\u212aÉ") {
		t.Fatalf("variable-length variants: got %d,%d", a, b)
	}
}

func TestUnicodeFilterTerms(t *testing.T) {
	rec := &engine.LogRecord{Message: "Fehler in der ÉCOLE-Verbindung", Raw: engine.RawRecord{Data: "Fehler in der ÉCOLE-Verbindung"}, SourceID: "Écoles.log"}
	for _, q := range []string{"école", "message:école", "source:écoles", "-straße"} {
		p, err := Compile(q)
		if err != nil || !p.Match(rec) {
			t.Errorf("query %q should match: err=%v", q, err)
		}
		if pf := RawPrefilter(p); pf != nil && !pf(rec.Raw.Data) {
			t.Errorf("query %q: prefilter rejected a matching record", q)
		}
	}
}

func BenchmarkFoldMatcherUnicodeMiss(b *testing.B) {
	s := strings.Repeat("2026-10-15 10:00:00 INFO Verarbeitung abgeschlossen für Benutzer Müller ", 2)
	fm := NewFoldMatcher("ÉCOLE-nicht-vorhanden")
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		fm.Contains(s)
	}
}
