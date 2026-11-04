package engine

import (
	"strings"
	"testing"
)

func TestStripANSI(t *testing.T) {
	const esc = "\x1b"
	for in, want := range map[string]string{
		"plain text":                                                      "plain text",
		esc + "[32mINFO" + esc + "[0m started":                            "INFO started",
		esc + "[2m2026-10-15T10:00:00Z" + esc + "[0;39m":                  "2026-10-15T10:00:00Z",
		esc + "[1;31;40mERROR" + esc + "[m x":                             "ERROR x",
		esc + "[38;5;208morange" + esc + "[0m":                            "orange",
		esc + "[38;2;255;100;0mtruecolor" + esc + "[0m":                   "truecolor",
		"a" + esc + "[2Kb" + esc + "[1Gc":                                 "abc", // erase line / cursor column
		esc + "]0;window title\x07after":                                  "after",
		esc + "]8;;http://x" + esc + "\\link" + esc + "]8;;" + esc + "\\": "link",
		esc + "(Btext":                 "text",
		"trailing" + esc:               "trailing",
		esc + "[":                      "",
		"keep \t tabs and unicode é 😀": "keep \t tabs and unicode é 😀",
	} {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
		}
	}
	if testing.AllocsPerRun(100, func() { StripANSI("an ordinary log line") }) != 0 {
		t.Error("text without escapes must not allocate")
	}
}

func TestSanitizeLeavesNoColourDebris(t *testing.T) {
	got := SanitizeText("\x1b[31mred\x1b[0m and \x1b[1;32mgreen\x1b[0m")
	if got != "red and green" {
		t.Fatalf("got %q", got)
	}
	if strings.ContainsAny(SanitizeText("\x1b]0;evil title\x07ok"), "\x1b\x07") {
		t.Fatal("OSC sequences must be removed")
	}
}

func FuzzStripANSI(f *testing.F) {
	f.Add("\x1b[32mINFO\x1b[0m")
	f.Add("\x1b]0;t\x07x\x1b")
	f.Add("\x1b[1;2;3")
	f.Fuzz(func(t *testing.T, s string) {
		out := StripANSI(s)
		if strings.IndexByte(out, 0x1b) >= 0 && strings.IndexByte(s, 0x1b) < 0 {
			t.Fatal("output invented an ESC")
		}
		if len(out) > len(s) {
			t.Fatalf("output longer than input: %q -> %q", s, out)
		}
		if strings.IndexByte(s, 0x1b) < 0 && out != s {
			t.Fatal("input without ESC must be unchanged")
		}
		// idempotent apart from sequences that only form after earlier ones are removed
		if StripANSI(out) != StripANSI(StripANSI(out)) {
			t.Fatal("not stable")
		}
	})
}
