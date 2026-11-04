package input

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/parser"
)

func TestDetectStyle(t *testing.T) {
	for name, c := range map[string]struct {
		in   string
		want decorStyle
	}{
		"compose, text timestamps": {"web-1  | 2026-10-15 10:00:00 INFO started\ndb-1   | 2026-10-15 10:00:01 ERROR disk full\nweb-1  | 2026-10-15 10:00:02 WARN slow\n", decorStyle{src: srcCompose}},
		"compose, plain lines":     {"web-1  | GET /health 200\ndb-1   | checkpoint starting\n", decorStyle{src: srcCompose}},
		"compose, one line":        {"project-web-1  | hello\n", decorStyle{src: srcCompose}},
		"compose v1 names":         {"web_1  | a\ndb_1   | b\n", decorStyle{src: srcCompose}},
		"compose with colour":      {"\x1b[36mweb-1  |\x1b[0m hello\n\x1b[33mdb-1   |\x1b[0m world\n", decorStyle{src: srcCompose}},
		"compose + docker -t":      {"web-1  | 2026-10-15T10:00:00.1Z 2026-10-15 10:00:00 ERROR boom\nweb-1  | 2026-10-15T10:00:00.1Z \tat A.b(A.java:1)\n", decorStyle{src: srcCompose, stamp: true}},
		"kubectl --prefix":         {"[pod/web-7d9f/web] 2026-10-15T10:00:00Z level=info msg=x\n[pod/db-0/postgres] 2026-10-15T10:00:01Z level=error msg=y\n", decorStyle{src: srcKube}},
		"kubectl --prefix, short":  {"[web-7d9f/web] hello\n", decorStyle{src: srcKube}},
		"docker -t, stack trace":   {"2026-10-15T10:00:00Z 2026-10-15 10:00:00 ERROR boom\n2026-10-15T10:00:00Z \tat A.b(A.java:1)\n2026-10-15T10:00:01Z 2026-10-15 10:00:01 INFO next\n", decorStyle{stamp: true}},
		// look-alikes that must NOT be treated as decorated
		"docker -t, plain app":       {"2026-10-15T10:00:00Z INFO started\n2026-10-15T10:00:01Z ERROR failed\n", decorStyle{}},
		"ordinary iso-Z log":         {"2026-10-15T10:00:00Z INFO started\n\tat Foo.bar\n2026-10-15T10:00:01Z ERROR failed\n", decorStyle{}},
		"bracketed timestamp":        {"[2026-10-15T10:00:00Z] started\n[2026-10-15T10:00:01Z] failed\n", decorStyle{}},
		"bracketed level":            {"[INFO] started\n[ERROR] failed\n", decorStyle{}},
		"table, two rows":            {"id | name\n1  | alice\n", decorStyle{}},
		"ordinary log":               {"2026-10-15 10:00:00 INFO started\n2026-10-15 10:00:01 ERROR failed\n", decorStyle{}},
		"json lines":                 {"{\"level\":\"info\",\"msg\":\"a | b\"}\n{\"level\":\"warn\",\"msg\":\"c\"}\n", decorStyle{}},
		"mostly plain, one prefixed": {"hello\nworld\nweb-1  | x\nfoo\nbar\n", decorStyle{}},
		"empty":                      {"", decorStyle{}},
	} {
		if got := detectStyle([]byte(c.in)); got != c.want {
			t.Errorf("%s: detected %+v, want %+v", name, got, c.want)
		}
	}
}

func TestStripLine(t *testing.T) {
	compose := decorStyle{src: srcCompose, stamp: true}
	src, rest, hint := compose.strip("web-1  | 2026-10-15T10:00:00.5Z 2026-10-15 10:00:00 ERROR boom")
	if src != "web-1" || rest != "2026-10-15 10:00:00 ERROR boom" || hint.IsZero() {
		t.Fatalf("got %q %q %v", src, rest, hint)
	}
	if s, r, _ := (decorStyle{src: srcKube}).strip("[pod/web-7d9f/web] hello"); s != "web-7d9f/web" || r != "hello" {
		t.Fatalf("kube: %q %q", s, r)
	}
	if s, r, _ := compose.strip("Attaching to web-1, db-1"); s != "" || r != "Attaching to web-1, db-1" {
		t.Fatalf("a line without the prefix is left alone: %q %q", s, r)
	}
}

type streamRig struct {
	ring *engine.RingStore
	sink *recSink
	w    *io.PipeWriter
	tl   *Tailer
	stop func()
}

func newStream(t *testing.T) *streamRig { return newStreamOpts(t, false) }

func newStreamOpts(t *testing.T, lineFraming bool) *streamRig {
	t.Helper()
	reg := parser.NewRegistry()
	parser.RegisterCore(reg)
	pr, pw := io.Pipe()
	r := &streamRig{ring: newRing(), sink: &recSink{}, w: pw}
	tl, err := NewTailer(TailOptions{SourceID: "stdin", Registry: reg, Store: r.ring, Sink: r.sink, Reader: pr,
		IdleFlush: 40 * time.Millisecond, Coalesce: 5 * time.Millisecond, LineFraming: lineFraming})
	if err != nil {
		t.Fatal(err)
	}
	r.tl = tl
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()
	r.stop = func() { pw.Close(); cancel(); <-done }
	t.Cleanup(r.stop)
	return r
}

func (r *streamRig) records() []engine.LogRecord {
	var out []engine.LogRecord
	r.ring.Range(0, 0, func(x *engine.LogRecord) bool { out = append(out, *x); return true })
	return out
}

func TestComposeStreamBecomesPerServiceRecords(t *testing.T) {
	r := newStream(t)
	// two services interleave; the stack trace of web-1 is split by a line from db-1
	r.w.Write([]byte("web-1  | 2026-10-15 10:00:00 INFO started\n" +
		"db-1   | 2026-10-15 10:00:01 ERROR disk full\n" +
		"web-1  | 2026-10-15 10:00:02 ERROR boom\n" +
		"web-1  | \tat A.b(A.java:1)\n" +
		"db-1   | 2026-10-15 10:00:03 WARN slow\n" +
		"web-1  | \tat C.d(C.java:2)\n" +
		"web-1  | 2026-10-15 10:00:04 INFO next\n"))
	waitFor(t, "records", func() bool { return r.sink.count() == 5 })
	byMsg := map[string]engine.LogRecord{}
	for _, rec := range r.records() {
		byMsg[strings.SplitN(rec.Message, "\n", 2)[0]] = rec
	}
	started := byMsg["started"]
	if started.SourceID != "web-1" || started.Severity != engine.SevInfo || started.Timestamp.IsZero() {
		t.Fatalf("service, level and the application's own timestamp must come through: %+v", started)
	}
	boom := byMsg["boom"]
	if boom.SourceID != "web-1" || boom.Severity != engine.SevError || !strings.Contains(boom.Raw.Data, "A.java:1") || !strings.Contains(boom.Raw.Data, "C.java:2") {
		t.Fatalf("the stack trace of web-1 must stay together despite db-1 lines in between: %q", boom.Raw.Data)
	}
	if byMsg["disk full"].SourceID != "db-1" || byMsg["slow"].SourceID != "db-1" || byMsg["slow"].Severity != engine.SevWarn {
		t.Fatalf("db-1 records: %+v %+v", byMsg["disk full"], byMsg["slow"])
	}
	if strings.Contains(boom.Raw.Data, "web-1") {
		t.Fatalf("the prefix must be gone from the record: %q", boom.Raw.Data)
	}
	if r.tl.Sources() != 2 {
		t.Fatalf("sources = %d, want 2", r.tl.Sources())
	}
}

func TestKubectlPrefixStream(t *testing.T) {
	r := newStream(t)
	r.w.Write([]byte("[pod/web-7d9f/web] 2026-10-15T10:00:00Z level=info msg=\"listening\"\n[pod/db-0/postgres] 2026-10-15T10:00:01Z level=error msg=\"oops\"\n"))
	waitFor(t, "records", func() bool { return r.sink.count() == 2 })
	recs := r.records()
	if recs[0].SourceID != "web-7d9f/web" || recs[1].SourceID != "db-0/postgres" || recs[1].Severity != engine.SevError || recs[0].Timestamp.IsZero() {
		t.Fatalf("kubectl --prefix: %+v | %+v", recs[0], recs[1])
	}
}

func TestDockerTimestampsOnEveryLineKeepStackTracesTogether(t *testing.T) {
	r := newStream(t)
	r.w.Write([]byte("2026-10-15T10:00:00Z 2026-10-15 10:00:00 ERROR boom\n2026-10-15T10:00:00Z \tat A.b(A.java:1)\n2026-10-15T10:00:00Z \tat C.d(C.java:2)\n2026-10-15T10:00:01Z 2026-10-15 10:00:01 INFO next\n"))
	waitFor(t, "records", func() bool { return r.sink.count() == 2 })
	recs := r.records()
	if recs[0].Severity != engine.SevError || !strings.Contains(recs[0].Raw.Data, "C.java:2") || recs[0].SourceID != "stdin" {
		t.Fatalf("docker -t must not split a stack trace: %+v", recs[0])
	}
	if recs[1].Message != "next" {
		t.Fatalf("second record: %+v", recs[1])
	}
}

func TestToolingTimeIsUsedWhenTheTextHasNone(t *testing.T) {
	r := newStreamOpts(t, true) // one record per line, so the second line stands alone
	r.w.Write([]byte("web-1  | 2026-10-15T10:00:00.5Z 2026-10-15 10:00:00 INFO has its own timestamp\n" +
		"web-1  | 2026-10-15T10:00:01.5Z plain line without one\n"))
	waitFor(t, "records", func() bool { return r.sink.count() == 2 })
	recs := r.records()
	if recs[0].Timestamp.Format("15:04:05.0") != "10:00:00.0" {
		t.Fatalf("the application's own timestamp wins: %v", recs[0].Timestamp)
	}
	if recs[1].Message != "plain line without one" || recs[1].Timestamp.Format("15:04:05.0") != "10:00:01.5" {
		t.Fatalf("a line with no timestamp of its own takes the tooling's: %+v", recs[1])
	}
}

// Ordinary piped logs must behave exactly as before.
func TestOrdinaryStreamsAreNotDecorated(t *testing.T) {
	for name, in := range map[string]string{
		"iso-Z with stack trace": "2026-10-15T10:00:00Z ERROR boom\n\tat A.b(A.java:1)\n2026-10-15T10:00:01Z INFO next\n",
		"bracketed timestamps":   "[2026-10-15T10:00:00Z] one\n[2026-10-15T10:00:01Z] two\n",
		"plain":                  "2026-10-15 10:00:00 INFO a\n2026-10-15 10:00:01 INFO b\n",
	} {
		r := newStream(t)
		r.w.Write([]byte(in))
		r.w.Close()
		waitFor(t, name, func() bool { return r.sink.last() == "ended" })
		if r.tl.Sources() != 0 || r.tl.dec != nil {
			t.Errorf("%s: wrongly treated as decorated", name)
		}
		for _, rec := range r.records() {
			if rec.SourceID != "stdin" {
				t.Errorf("%s: source %q", name, rec.SourceID)
			}
		}
	}
	// the iso-Z log keeps its stack trace in one record
	r := newStream(t)
	r.w.Write([]byte("2026-10-15T10:00:00Z ERROR boom\n\tat A.b(A.java:1)\n2026-10-15T10:00:01Z INFO next\n"))
	r.w.Close()
	waitFor(t, "ended", func() bool { return r.sink.last() == "ended" })
	if recs := r.records(); len(recs) != 2 || !strings.Contains(recs[0].Raw.Data, "A.java:1") {
		t.Fatalf("an ordinary log must keep multiline grouping: %d records", len(recs))
	}
}

func TestDecoratedStreamEndsCleanly(t *testing.T) {
	r := newStream(t)
	r.w.Write([]byte("web-1  | 2026-10-15 10:00:00 INFO last line without newline"))
	r.w.Close()
	waitFor(t, "end of input", func() bool { return r.sink.last() == "ended" })
	if recs := r.records(); len(recs) != 1 || recs[0].SourceID != "web-1" {
		t.Fatalf("the unterminated last line must still be delivered: %+v", recs)
	}
}

// The records of a decorated stream must not depend on how it was cut into reads.
func TestDecoratedStreamIgnoresReadBoundaries(t *testing.T) {
	var in strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&in, "web-1  | 2026-10-15 10:%02d:00 INFO request %d\n", i, i)
		if i%7 == 0 {
			fmt.Fprintf(&in, "web-1  | \tat Handler.run(Handler.java:%d)\n", i)
		}
		fmt.Fprintf(&in, "db-1   | 2026-10-15 10:%02d:30 WARN slow query %d\n", i, i)
	}
	input := in.String()
	perSource := func(chunk int) map[string]string {
		r := newStream(t)
		for p := 0; p < len(input); p += chunk {
			end := p + chunk
			if end > len(input) {
				end = len(input)
			}
			r.w.Write([]byte(input[p:end]))
		}
		r.w.Close()
		waitFor(t, "all 120 records delivered", func() bool { return r.sink.count() == 120 })
		out := map[string]string{}
		for _, rec := range r.records() {
			out[rec.SourceID] += fmt.Sprintf("%s|%v|%s;", rec.Timestamp.Format("15:04:05"), rec.Severity, rec.Raw.Data)
		}
		return out
	}
	want := perSource(len(input) + 1)
	if len(want["web-1"]) == 0 || len(want["db-1"]) == 0 {
		t.Fatalf("both services expected: %d %d", len(want["web-1"]), len(want["db-1"]))
	}
	for _, chunk := range []int{1, 3, 7, 50, 333} {
		got := perSource(chunk)
		if got["web-1"] != want["web-1"] || got["db-1"] != want["db-1"] {
			t.Fatalf("records differ when read %d bytes at a time", chunk)
		}
	}
}

// Whatever bytes are piped in, detection and stripping must not panic or loop.
func FuzzDetectAndStrip(f *testing.F) {
	for _, s := range []string{"web-1  | hello\n", "[pod/a/b] x\n", "2026-10-15T10:00:00Z 2026-10-15 10:00:00 E\n", "\x1b[31m|\x1b[0m", "|||\n\n", "a | b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		st := detectStyle([]byte(s))
		if st.active() {
			st.stripSample([]byte(s))
		}
		for _, l := range strings.Split(s, "\n") {
			decorStyle{src: srcCompose, stamp: true}.strip(l)
			decorStyle{src: srcKube}.strip(l)
		}
	})
}
