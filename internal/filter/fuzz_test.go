package filter

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/asyncpoint/logi/internal/engine"
)

// ContainsFold / FoldMatcher must agree with a straightforward reference for
// arbitrary input: byte-wise ASCII folding for ASCII needles, and
// strings.EqualFold over rune-aligned substrings for non-ASCII needles.
func FuzzContainsFold(f *testing.F) {
	f.Add("Hello WORLD", "world")
	f.Add("aaaaab", "aab")
	f.Add("", "")
	f.Add("x", "xyz")
	f.Add("ÉCOLE publique", "école")
	f.Add("ΣΑΣ", "σας")
	f.Fuzz(func(t *testing.T, s, sub string) {
		fm := NewFoldMatcher(sub)
		var want bool
		switch {
		case isASCII(sub):
			fold := func(x string) string {
				b := []byte(x)
				for i, c := range b {
					if c >= 'A' && c <= 'Z' {
						b[i] = c + 32
					}
				}
				return string(b)
			}
			want = strings.Contains(fold(s), fold(sub))
			if got := ContainsFold(s, fold(sub)); got != want {
				t.Fatalf("ContainsFold(%q, %q) = %v, want %v", s, sub, got, want)
			}
		case utf8.ValidString(sub) && utf8.ValidString(s):
			for i := 0; i <= len(s) && !want; i++ {
				if i < len(s) && !utf8.RuneStart(s[i]) {
					continue
				}
				for j := i; j <= len(s) && !want; j++ {
					if (j == len(s) || utf8.RuneStart(s[j])) && strings.EqualFold(s[i:j], sub) {
						want = true
					}
				}
			}
		default:
			return // invalid UTF-8 with a non-ASCII needle: byte semantics, not worth a reference
		}
		if got := fm.Contains(s); got != want {
			t.Fatalf("FoldMatcher(%q).Contains(%q) = %v, want %v", sub, s, got, want)
		}
		if a, b := fm.Index(s); (a >= 0) != want || (a >= 0 && (a > b || b > len(s))) {
			t.Fatalf("Index(%q,%q) = %d,%d, want found=%v", sub, s, a, b, want)
		}
	})
}

// Compile must never panic, and RawPrefilter must be a *necessary* condition:
// if the full predicate accepts a record, the prefilter must accept its raw
// text. A violation would make stores silently skip real matches.
func FuzzFilterPrefilterIsNecessary(f *testing.F) {
	f.Add("error timeout", "2026-10-15 10:00:00 ERROR Timeout talking to db")
	f.Add("re:(?i)t.meout severity:>=warn", "WARN timeout")
	f.Add(`-debug "two words"`, "two words here")
	f.Add("status=ok", `{"msg":"x","status":"ok"}`)
	f.Fuzz(func(t *testing.T, q, raw string) {
		p, err := Compile(q)
		if err != nil || p == nil {
			return
		}
		for _, sev := range []engine.Severity{engine.SevUnknown, engine.SevInfo, engine.SevError} {
			rec := &engine.LogRecord{Severity: sev, Message: raw, Raw: engine.RawRecord{Data: raw}}
			if p.Match(rec) {
				if pf := RawPrefilter(p); pf != nil && !pf(raw) {
					t.Fatalf("prefilter rejected a matching record: query %q raw %q", q, raw)
				}
			}
		}
	})
}
