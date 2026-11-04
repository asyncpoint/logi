package engine

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeText(t *testing.T) {
	if got := SanitizeText("plain ünïcode 😀 ok"); got != "plain ünïcode 😀 ok" {
		t.Fatalf("clean text changed: %q", got)
	}
	got := SanitizeText("a\x1b[31m\u009b31m\u202eevil\u2028\x07\xffz")
	for _, bad := range []string{"\x1b", "\u009b", "\u202e", "\u2028", "\x07", "\xff"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	if !utf8.ValidString(got) {
		t.Errorf("result not valid UTF-8: %q", got)
	}
	if n := testing_AllocsClean(); n != 0 {
		t.Errorf("clean path allocated %v times", n)
	}
}

func testing_AllocsClean() float64 {
	return testing.AllocsPerRun(100, func() { SanitizeText("an ordinary log line") })
}

func TestSanitizeKeepTabs(t *testing.T) {
	if got := SanitizeKeepTabs("a\tb\r"); got != "a    b" {
		t.Errorf("got %q", got)
	}
}

// A multi-line value (a stack trace) must keep its line breaks; only unsafe
// characters inside a line are neutralised.
func TestSanitizeKeepTabsKeepsLineBreaks(t *testing.T) {
	cases := map[string]string{
		"first\nsecond\n   at Foo.Bar()":      "first\nsecond\n   at Foo.Bar()",
		"crlf one\r\ncrlf two\r\n":            "crlf one\ncrlf two\n",
		"a\tb\n\tc":                           "a    b\n    c",
		"red \x1b[31mtext\x1b[0m\nnext":       "red text\nnext",
		"bell\x07 here\nbad \x1b]0;title\x07": "bell  here\nbad ",
		"":                                    "",
		"\n":                                  "\n",
	}
	for in, want := range cases {
		if got := SanitizeKeepTabs(in); got != want {
			t.Errorf("SanitizeKeepTabs(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzSanitizeKeepTabs(f *testing.F) {
	f.Add("a\nb\r\n\x1b[0m\u009b\xff\t")
	f.Fuzz(func(t *testing.T, s string) {
		out := SanitizeKeepTabs(s)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 for %q", s)
		}
		if got, want := strings.Count(out, "\n"), strings.Count(s, "\n"); got != want {
			t.Fatalf("line breaks changed from %d to %d for %q", want, got, s)
		}
		for _, r := range out {
			if r != '\n' && unsafeRune(r) {
				t.Fatalf("unsafe rune %U survived in %q", r, out)
			}
		}
	})
}

func FuzzSanitizeText(f *testing.F) {
	f.Add("a\x1b[0m\u009b\xff")
	f.Fuzz(func(t *testing.T, s string) {
		out := SanitizeText(s)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 for %q", s)
		}
		for _, r := range out {
			if unsafeRune(r) {
				t.Fatalf("unsafe rune %U in %q", r, out)
			}
		}
	})
}

func BenchmarkSanitizeClean(b *testing.B) {
	s := "2026-10-15 10:00:00 INFO request handled path=/api/v1/items/42"
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		SanitizeText(s)
	}
}
