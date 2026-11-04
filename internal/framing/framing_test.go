package framing

import (
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

type got struct {
	text      string
	off, ln   int64
	lines     int
	truncated bool
}

func collect(f Framer, chunks ...string) (out []got) {
	emit := func(fr *Frame) {
		out = append(out, got{string(fr.Data), fr.Offset, fr.Length, fr.Lines, fr.Truncated})
	}
	for _, c := range chunks {
		f.Push([]byte(c), emit)
	}
	return
}

func TestLineFramerBasicAndCRLF(t *testing.T) {
	f := NewLineFramer(engine.DefaultLimits(), 0)
	out := collect(f, "a\r\n\nbb\n")
	if len(out) != 2 || out[0].text != "a" || out[1].text != "bb" {
		t.Fatalf("got %+v", out)
	}
	if out[0].off != 0 || out[0].ln != 3 || out[1].off != 4 || out[1].ln != 3 {
		t.Fatalf("offsets wrong: %+v", out)
	}
}

func TestPartialLineIsBufferedAcrossPushes(t *testing.T) {
	f := NewLineFramer(engine.DefaultLimits(), 100)
	out := collect(f, "2026-09-18 INFO Request sta", "rted succ", "essfully\nnext\n")
	if len(out) != 2 || out[0].text != "2026-09-18 INFO Request started successfully" || out[1].text != "next" {
		t.Fatalf("got %+v", out)
	}
	if out[0].off != 100 {
		t.Fatalf("offset = %d, want 100", out[0].off)
	}
}

func TestUnterminatedTailOnlyOnFinalFlush(t *testing.T) {
	f := NewLineFramer(engine.DefaultLimits(), 0)
	var n int
	emit := func(*Frame) { n++ }
	f.Push([]byte("done\npartial"), emit)
	f.Flush(false, emit)
	if n != 1 {
		t.Fatalf("partial emitted prematurely: %d", n)
	}
	f.Flush(true, emit)
	if n != 2 {
		t.Fatalf("final flush should emit partial line, n=%d", n)
	}
}

func TestMultilineStackTrace(t *testing.T) {
	f := NewMultilineFramer(engine.DefaultLimits(), 0, LooksLikeTimestamp)
	in := "2026-09-18 ERROR Request failed\nSystem.Exception: Something went wrong\n   at Foo.Bar()\n   at Foo.Baz()\n2026-09-18 INFO ok\n"
	out := collect(f, in)
	if len(out) != 1 {
		t.Fatalf("second record must stay open until next start/flush, got %d", len(out))
	}
	if out[0].lines != 4 || !strings.Contains(out[0].text, "at Foo.Baz()") {
		t.Fatalf("bad multiline record: %+v", out[0])
	}
	var last []got
	f.Flush(false, func(fr *Frame) { last = append(last, got{text: string(fr.Data)}) })
	if len(last) != 1 || last[0].text != "2026-09-18 INFO ok" {
		t.Fatalf("idle flush: %+v", last)
	}
	if f.Pending() {
		t.Fatal("nothing should be pending after flush")
	}
}

func TestMultilineSpanCoversWholeRecord(t *testing.T) {
	f := NewMultilineFramer(engine.DefaultLimits(), 0, LooksLikeTimestamp)
	in := "2026-01-01 00:00:00 A\n  cont\n2026-01-01 00:00:01 B\n"
	out := collect(f, in)
	if len(out) != 1 || out[0].off != 0 || out[0].ln != int64(len("2026-01-01 00:00:00 A\n  cont\n")) {
		t.Fatalf("%+v", out)
	}
}

func TestLimitsTruncateButKeepOffsetsExact(t *testing.T) {
	lim := engine.Limits{MaxRecordBytes: 16, MaxLines: 3}
	f := NewLineFramer(lim, 0)
	out := collect(f, strings.Repeat("x", 100), "y\nshort\n")
	if len(out) != 2 || !out[0].truncated || len(out[0].text) != 16 || out[0].ln != 102 {
		t.Fatalf("%+v", out)
	}
	if out[1].off != 102 || out[1].truncated {
		t.Fatalf("offset after truncated record wrong: %+v", out[1])
	}
	m := NewMultilineFramer(lim, 0, LooksLikeTimestamp)
	out = collect(m, "2026-01-01 00:00:00 A\nb\nc\nd\ne\n2026-01-01 00:00:01 B\n")
	if len(out) != 1 || !out[0].truncated || out[0].lines != 5 {
		t.Fatalf("multiline limit: %+v", out)
	}
}

func TestLooksLikeTimestamp(t *testing.T) {
	yes := []string{"2026-09-19 11:32:04 x", "2026-09-19T11:32:04Z", "[2026/09/19 11:32] x", "11:32:04 INFO", "Sep 19 11:32:04 host", "19-09-2026 11:32", "(2026-09-19)  z"}
	no := []string{"", "   at Foo.Bar()", "System.Exception: boom", "{\"a\":1}", "Caused by: x", "2026-9-19"}
	for _, s := range yes {
		if !LooksLikeTimestamp([]byte(s)) {
			t.Errorf("want timestamp: %q", s)
		}
	}
	for _, s := range no {
		if LooksLikeTimestamp([]byte(s)) {
			t.Errorf("want non-timestamp: %q", s)
		}
	}
}

func TestDetectChoosesFramer(t *testing.T) {
	if _, ok := Detect([]byte("2026-09-19 11:32:04 a\n  b\n2026-09-19 11:32:05 c\n"), engine.DefaultLimits(), 0).(*MultilineFramer); !ok {
		t.Fatal("expected multiline framer for timestamped log")
	}
	if _, ok := Detect([]byte("hello\nworld\n"), engine.DefaultLimits(), 0).(*LineFramer); !ok {
		t.Fatal("expected line framer for plain log")
	}
}

// Chunk boundaries must never change the framing result.
func TestFramingIsChunkingInvariant(t *testing.T) {
	in := "2026-01-01 00:00:00 A\n  cont\r\n\n2026-01-01 00:00:01 B\nlast-no-newline"
	whole := collect(NewMultilineFramer(engine.DefaultLimits(), 0, LooksLikeTimestamp), in)
	for size := 1; size < len(in); size += 3 {
		f := NewMultilineFramer(engine.DefaultLimits(), 0, LooksLikeTimestamp)
		var chunks []string
		for i := 0; i < len(in); i += size {
			e := i + size
			if e > len(in) {
				e = len(in)
			}
			chunks = append(chunks, in[i:e])
		}
		out := collect(f, chunks...)
		f.Flush(true, func(fr *Frame) { out = append(out, got{string(fr.Data), fr.Offset, fr.Length, fr.Lines, fr.Truncated}) })
		w := append([]got(nil), whole...)
		fw := NewMultilineFramer(engine.DefaultLimits(), 0, LooksLikeTimestamp)
		w = collect(fw, in)
		fw.Flush(true, func(fr *Frame) { w = append(w, got{string(fr.Data), fr.Offset, fr.Length, fr.Lines, fr.Truncated}) })
		if len(out) != len(w) {
			t.Fatalf("chunk=%d: %d records, want %d", size, len(out), len(w))
		}
		for i := range w {
			if out[i] != w[i] {
				t.Fatalf("chunk=%d rec %d: %+v != %+v", size, i, out[i], w[i])
			}
		}
	}
}

func BenchmarkLineFramer(b *testing.B) {
	line := strings.Repeat("2026-09-19 11:32:04 INFO something happened here\n", 2000)
	data := []byte(line)
	f := NewLineFramer(engine.DefaultLimits(), 0)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.Push(data, func(*Frame) {})
	}
}

func TestTerminatorClosesTheRecordAndIsDropped(t *testing.T) {
	m := NewMultilineFramer(engine.DefaultLimits(), 0, LooksLikeTimestamp)
	m.SetTerminator(func(l []byte) bool { return string(l) == "END" })
	in := "2026-09-20 06:22:56 first entry\n\nEND\n2026-09-20 06:22:57 second entry\nEND\nEND\n2026-09-20 06:22:58 third\n"
	out := collect(m, in)
	m.Flush(true, func(fr *Frame) { out = append(out, got{string(fr.Data), fr.Offset, fr.Length, fr.Lines, fr.Truncated}) })
	if len(out) != 3 {
		t.Fatalf("want 3 records, got %+v", out)
	}
	if out[0].text != "2026-09-20 06:22:56 first entry" || out[1].text != "2026-09-20 06:22:57 second entry" {
		t.Fatalf("END/blank must not become part of a record: %+v", out)
	}
	// the terminator's bytes still belong to the record's span, so spans stay contiguous
	if out[0].off != 0 || out[0].ln != int64(len("2026-09-20 06:22:56 first entry\n\nEND\n")) || out[1].off != out[0].ln {
		t.Fatalf("spans: %+v", out)
	}
	// a stray terminator with no open record is simply dropped
	m2 := NewMultilineFramer(engine.DefaultLimits(), 0, LooksLikeTimestamp)
	m2.SetTerminator(func(l []byte) bool { return string(l) == "END" })
	if o := collect(m2, "END\n"); len(o) != 0 {
		t.Fatalf("%+v", o)
	}
}

func TestMultilineCapLeavesNoHole(t *testing.T) {
	lim := engine.Limits{MaxRecordBytes: 100, MaxLines: 2000}
	in := "2026-10-15 10:00:00 ERROR boom\n  at A\n  " + strings.Repeat("x", 90) + "\n  at C\n2026-10-15 10:00:01 INFO next\n"
	var got []string
	var trunc []bool
	emit := func(f *Frame) { got = append(got, string(f.Data)); trunc = append(trunc, f.Truncated) }
	fr := NewMultilineFramer(lim, 0, LooksLikeTimestamp)
	fr.Push([]byte(in), emit)
	fr.Flush(true, emit)
	if len(got) != 2 || strings.Contains(got[0], "at C") || !trunc[0] || trunc[1] {
		t.Fatalf("got %q trunc=%v; want clean prefix, truncated flag only on first", got, trunc)
	}
}

// Coloured output (docker, kubectl, coloured loggers) puts escape sequences in
// front of the timestamp; the line must still start a new record.
func TestColouredLinesStartRecords(t *testing.T) {
	for in, want := range map[string]bool{
		"\x1b[32m2026-09-19 10:00:00\x1b[0m INFO a":      true,
		"\x1b[1;32m\x1b[2m2026-09-19 10:00:00 INFO a":    true,
		"\x1b[90m[2026-09-19 10:00:00]\x1b[0m INFO a":    true,
		"\x1b[90m[\x1b[2m2026-09-19 10:00:00] INFO a":    true,
		"\x1b[31m    at com.example.Foo(Foo.java:1)":     false,
		"\x1b[31mjust red text":                          false,
		"\x1b[32m":                                       false,
		"\x1b":                                           false,
		"\x1b[32m11:32:04 started":                       true,
		"\x1b[32mSep 19 10:00:00 host sshd[1]: accepted": true,
	} {
		if got := LooksLikeTimestamp([]byte(in)); got != want {
			t.Errorf("LooksLikeTimestamp(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestColouredLogsFrameIntoSeparateRecords(t *testing.T) {
	in := "\x1b[32m2026-09-19 10:00:01\x1b[0m INFO one\n" +
		"\x1b[32m2026-09-19 10:00:02\x1b[0m \x1b[31mERROR\x1b[0m two\n" +
		"\x1b[31m  at stack frame\x1b[0m\n" +
		"2026-09-19 10:00:03 INFO plain three\n"
	f := NewMultilineFramer(engine.Limits{MaxRecordBytes: 1 << 20, MaxLines: 100}, 0, LooksLikeTimestamp)
	var got []string
	emit := func(x *Frame) { got = append(got, string(x.Data)) }
	f.Push([]byte(in), emit)
	f.Flush(true, emit)
	if len(got) != 3 {
		t.Fatalf("got %d records, want 3: %q", len(got), got)
	}
	if !strings.Contains(got[1], "at stack frame") {
		t.Fatalf("continuation lost: %q", got[1])
	}
}

// Prefixing colour codes must never change the answer for a line that does not
// itself begin with an escape.
func TestEscapePrefixDoesNotChangeTimestampVerdict(t *testing.T) {
	lines := []string{"2026-09-19 10:00:00 x", "plain", "  indented", "[2026-09-19 10:00:00] x", "(11:32:04) y", "Sep  9 10:00:00 h", "12", ""}
	for _, l := range lines {
		for _, esc := range []string{"\x1b[0m", "\x1b[1;31m", "\x1b[38;5;196m\x1b[1m"} {
			if LooksLikeTimestamp([]byte(esc+l)) != LooksLikeTimestamp([]byte(l)) {
				t.Errorf("escape prefix %q changed the verdict for %q", esc, l)
			}
		}
	}
}

func FuzzLooksLikeTimestampNeverPanics(f *testing.F) {
	f.Add("\x1b[32m2026-09-19 10:00:00 x")
	f.Add("\x1b\x1b\x1b[")
	f.Fuzz(func(t *testing.T, s string) { LooksLikeTimestamp([]byte(s)) })
}
