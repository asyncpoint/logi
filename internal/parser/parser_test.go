package parser

import (
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
)

func TestTimestampForms(t *testing.T) {
	cases := []struct {
		in   string
		want string
		n    int
	}{
		{"2026-09-19 11:32:04,123 INFO", "2026-09-19T11:32:04.123Z", 23},
		{"2026-09-19T11:32:04+05:00 x", "2026-09-19T11:32:04+05:00", 25},
		{"[2026/09/19 11:32:04] x", "2026-09-19T11:32:04Z", 21},
		{"19-09-2026 11:32:04", "2026-09-19T11:32:04Z", 19},
		{"2026-09-19 11:32:04 +0500 x", "2026-09-19T11:32:04+05:00", 25},
	}
	for _, c := range cases {
		ts, n, ok := ParseTimestampPrefix(c.in)
		if !ok || ts.Format("2006-01-02T15:04:05.999Z07:00") != c.want || n != c.n {
			t.Errorf("%q => %v n=%d ok=%v, want %s n=%d", c.in, ts, n, ok, c.want, c.n)
		}
	}
	if ts, _, ok := ParseTimestampPrefix("11:32:04 hi"); !ok || ts.Hour() != 11 {
		t.Error("time-only")
	}
	if ts, _, ok := ParseTimestampPrefix("Sep 19 11:32:04 host"); !ok || ts.Month() != time.September || ts.Day() != 19 {
		t.Error("syslog")
	}
	for _, bad := range []string{"", "hello", "2026-13-40 00:00:00", "12:99:00", "Foo 19 11:32:04 x"} {
		if _, _, ok := ParseTimestampPrefix(bad); ok {
			t.Errorf("should reject %q", bad)
		}
	}
}

func TestGenericParse(t *testing.T) {
	cases := []struct {
		in    string
		sev   engine.Severity
		msg   string
		multi bool
	}{
		{"2026-09-19 11:32:04 INFO Starting service", engine.SevInfo, "Starting service", false},
		{"2026-09-19 11:32:04,001 [worker-3] ERROR - Connection failed", engine.SevError, "Connection failed", false},
		{"11:32:03 WARN  Retry attempt 1", engine.SevWarn, "Retry attempt 1", false},
		{"2026-09-19T11:32:04Z level=debug msg=hi", engine.SevDebug, "msg=hi", false},
		{"plain text, nothing special", engine.SevUnknown, "plain text, nothing special", false},
		{"2026-09-18 ERROR Request failed\nSystem.Exception: x\n  at A()", engine.SevError, "Request failed\nSystem.Exception: x\n  at A()", true},
		{"[2026-09-19 11:32:04] [FATAL] boom", engine.SevFatal, "boom", false},
	}
	for _, c := range cases {
		r, err := Generic{}.Parse(c.in)
		if err != nil || r.Severity != c.sev || r.Message != c.msg || r.Multiline != c.multi {
			t.Errorf("%q => sev=%v msg=%q multi=%v err=%v", c.in, r.Severity, r.Message, r.Multiline, err)
		}
	}
}

func TestJSONParse(t *testing.T) {
	line := `{"time":"2026-09-18T10:00:00Z","level":"error","msg":"disk \"full\"","request":{"id":"a,b}","n":[1,2]},"duration":42}`
	r, err := JSON{}.Parse(line)
	if err != nil || r.Severity != engine.SevError || r.Message != `disk "full"` || r.Timestamp.Year() != 2026 {
		t.Fatalf("%+v %v", r, err)
	}
	f, err := Fields(line)
	if err != nil || f.GetValue("duration") != "42" || f.GetValue("msg") != `disk "full"` {
		t.Fatalf("fields: %+v %v", f, err)
	}
	if v, ok := Field(line, "duration"); !ok || v != "42" {
		t.Fatalf("Field: %q %v", v, ok)
	}
	if r, _ := (JSON{}).Parse(`{"ts":1758189600123,"level":50,"message":"x"}`); r.Severity != engine.SevError || r.Timestamp.Year() != 2025 {
		t.Fatalf("numeric level/epoch: %+v", r)
	}
}

func TestMalformedInputNeverLosesData(t *testing.T) {
	for _, in := range []string{`{"a":`, `{"a" 1}`, `not json`, `{"msg":"unterminated`, "{", `{"level":"info","msg":"ok"`, "\xff\xfe\x00 bad utf8 \xc3\x28"} {
		r, _ := JSON{}.Parse(in)
		if r.Message == "" && in != "" {
			t.Errorf("lost data for %q", in)
		}
	}
	r, err := JSON{}.Parse(`{"a":`)
	if err == nil || !r.ParseErr || r.Message != `{"a":` {
		t.Fatalf("expected flagged fallback: %+v %v", r, err)
	}
}

func TestRegistryDetection(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatal(err)
	}
	if reg.Detect([]byte("{\"a\":1}\n{\"b\":2}\n")).Name != "json" {
		t.Fatal("json not detected")
	}
	if reg.Detect([]byte("2026-09-19 INFO hi\n")).Name != "generic" {
		t.Fatal("generic fallback")
	}
	if err := reg.Register(GenericProfile()); err == nil {
		t.Fatal("duplicate registration must fail")
	}
}

func BenchmarkGenericParse(b *testing.B) {
	line := "2026-09-19 11:32:04,123 [worker-3] ERROR - Connection to database failed after 3 retries"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Generic{}.Parse(line)
	}
}

func BenchmarkJSONParse(b *testing.B) {
	line := `{"time":"2026-09-18T10:00:00Z","level":"error","msg":"request completed","request":{"id":"abc","n":[1,2,3]},"duration":42}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		JSON{}.Parse(line)
	}
}

var _ = strings.Repeat

func TestGenericLevelFormats(t *testing.T) {
	cases := []struct {
		in  string
		sev engine.Severity
		msg string
	}{
		{"2026-09-19 11:32:04.123 [VRB] tracing", engine.SevTrace, "tracing"},
		{"2026-09-19 11:32:04.123 [dbug] fine detail", engine.SevDebug, "fine detail"},
		{"2026-09-19 11:32:04 [INF] up", engine.SevInfo, "up"},
		{"2026-09-19 11:32:04 SUCCESS all done", engine.SevNotice, "all done"},
		{"2026-09-19 11:32:04 wrn low disk", engine.SevWarn, "low disk"},
		{"2026-09-19 11:32:04 fail request", engine.SevError, "request"},
		{"2026-09-19 11:32:04 CRIT out of memory", engine.SevCritical, "out of memory"},
		{"2026-09-19 11:32:04 [FTL] cannot continue", engine.SevFatal, "cannot continue"},
		{"2026-09-19 11:32:04 [E] bracketed letter", engine.SevError, "bracketed letter"},
		{"2026-09-19 11:32:04 (W) paren letter", engine.SevWarn, "paren letter"},
		{"2026-09-19 11:32:04 FINEST deep", engine.SevTrace, "deep"},
		{"E0920 11:32:04.123456 1234 server.go:12] boom", engine.SevError, "1234 server.go:12] boom"},
		{"I0920 11:32:04.000001 5 x.go:1] hello", engine.SevInfo, "5 x.go:1] hello"},
		{"2026-09-19 11:32:04 I am a sentence", engine.SevUnknown, "I am a sentence"},
	}
	for _, c := range cases {
		r, _ := Generic{}.Parse(c.in)
		if r.Severity != c.sev || r.Message != c.msg {
			t.Errorf("%q => %v %q, want %v %q", c.in, r.Severity, r.Message, c.sev, c.msg)
		}
	}
	if r, _ := (Generic{}).Parse("E0920 11:32:04.123456 1 f.go:1] x"); r.Timestamp.Month() != time.September {
		t.Fatal("glog timestamp")
	}
}

func TestNCacheEventLogTimestamp(t *testing.T) {
	ts, n, ok := ParseTimestampPrefix("2026-09-20 06:22:56, 110                        NCache")
	if !ok || ts.Format("2006-01-02 15:04:05.000") != "2026-09-20 06:22:56.110" || n != 24 {
		t.Fatalf("%v n=%d ok=%v", ts, n, ok)
	}
	// only ", ddd" (exactly three digits) is a fraction
	for _, in := range []string{"12:00:00, 5 files failed", "12:00:00, 5555", "12:00:00, 12 a"} {
		if _, n, ok := ParseTimestampPrefix(in); !ok || n != 8 {
			t.Errorf("%q must stop after the seconds, n=%d ok=%v", in, n, ok)
		}
	}
	if ts, n, _ := ParseTimestampPrefix("06:22:56, 110 x"); ts.Nanosecond() != 110_000_000 || n != 13 {
		t.Fatalf("time-only form: %v n=%d", ts, n)
	}
}

func TestJSONUnescapeRFC8259(t *testing.T) {
	cases := map[string]string{
		`{"level":"info","msg":"a\/b"}`:                 "a/b",
		`{"level":"info","msg":"x \ud83d\ude00 y"}`:     "x 😀 y",
		`{"level":"info","msg":"caf\u00e9 \"q\" \\ n"}`: `café "q" \ n`,
		`{"level":"info","msg":"lone \ud83d end"}`:      "lone \ufffd end",
		`{"level":"info","msg":"t\tn\nr\r"}`:            "t\tn\nr\r",
	}
	for in, want := range cases {
		r, err := JSON{}.Parse(in)
		if err != nil || r.Message != want {
			t.Errorf("Parse(%s) = %q, %v; want %q", in, r.Message, err, want)
		}
	}
}

func FuzzJSONUnescape(f *testing.F) {
	f.Add(`a\/b\ud83d\ude00\u00e9`)
	f.Fuzz(func(t *testing.T, s string) { jsonUnescape(s) })
}

func TestYearInference(t *testing.T) {
	ref := time.Date(2022, 3, 10, 12, 0, 0, 0, time.UTC)
	r, _ := Generic{Ref: ref}.Parse("Mar 09 10:00:00 host app: hello")
	if r.Timestamp.Year() != 2022 {
		t.Errorf("old log: year = %d, want 2022", r.Timestamp.Year())
	}
	// December lines in a file last written in January belong to last year.
	jan := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	r, _ = Generic{Ref: jan}.Parse("Dec 31 23:59:00 host app: hello")
	if r.Timestamp.Year() != 2025 {
		t.Errorf("rollover: year = %d, want 2025", r.Timestamp.Year())
	}
	g, _ := Generic{Ref: ref}.Parse("E0309 10:00:00.000000 1 f.go:1] boom")
	if g.Timestamp.Year() != 2022 {
		t.Errorf("glog year = %d, want 2022", g.Timestamp.Year())
	}
}

func TestZoneCache(t *testing.T) {
	line := `2026-10-15T10:00:00.123+05:30 INFO hello`
	if a := testing.AllocsPerRun(500, func() { Generic{}.Parse(line) }); a != 0 {
		t.Errorf("half-hour offset allocates %v/op", a)
	}
}

func TestDockerJSONFileLines(t *testing.T) {
	r, err := JSON{}.Parse(`{"log":"hello world\n","stream":"stdout","time":"2026-10-15T10:00:00.123456789Z"}`)
	if err != nil || r.Message != "hello world" || r.Timestamp.IsZero() || r.Timestamp.Year() != 2026 {
		t.Fatalf("docker json-file line: %+v %v", r, err)
	}
	// Docker records no level: it is read from the application's own text
	r, _ = JSON{}.Parse(`{"log":"2026-10-15 10:00:01 ERROR db down\n","stream":"stderr","time":"2026-10-15T10:00:01.000000000Z"}`)
	if r.Severity != engine.SevError || r.Message != "db down" {
		t.Fatalf("level should come from the inner text: sev=%v msg=%q", r.Severity, r.Message)
	}
	// a JSON line that has its own level keeps it
	r, _ = JSON{}.Parse(`{"log":"ERROR bad\n","level":"info","time":"2026-10-15T10:00:00Z"}`)
	if r.Severity != engine.SevInfo {
		t.Fatalf("an explicit level wins: %v", r.Severity)
	}
}

func TestCRILines(t *testing.T) {
	cases := []struct {
		line      string
		sev       engine.Severity
		msg, strm string
		flag      string
	}{
		{"2026-10-15T10:00:00.123456789Z stdout F hello world", engine.SevUnknown, "hello world", "stdout", "F"},
		{"2026-10-15T10:00:01.123456789Z stderr F ERROR something bad", engine.SevError, "something bad", "stderr", "F"},
		{"2026-10-15T10:00:02.000000000Z stdout P part one ", engine.SevUnknown, "part one ", "stdout", "P"},
		{`2026-10-15T10:00:03Z stdout F {"level":"warn","msg":"disk low","time":"2026-10-15T10:00:03Z"}`, engine.SevWarn, "disk low", "stdout", "F"},
	}
	for _, c := range cases {
		r, err := CRI{}.Parse(c.line)
		if err != nil || r.Severity != c.sev || r.Message != c.msg || r.Timestamp.IsZero() {
			t.Errorf("%q -> sev=%v msg=%q ts=%v err=%v", c.line, r.Severity, r.Message, r.Timestamp, err)
			continue
		}
		if r.Fields.GetValue("stream") != c.strm || r.Fields.GetValue("flag") != c.flag {
			t.Errorf("%q: fields %v", c.line, r.Fields)
		}
	}
	if r, _ := (CRI{}).Parse("not a cri line at all"); r.Message != "not a cri line at all" {
		t.Errorf("a non-CRI line must fall back to the generic view: %q", r.Message)
	}
}

func TestDetectionChoosesTheContainerFormats(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatal(err)
	}
	for sample, want := range map[string]string{
		"2026-10-15T10:00:00.1Z stdout F hello\n2026-10-15T10:00:01.1Z stderr F ERROR x\n":           "cri",
		`{"log":"hi\n","stream":"stdout","time":"2026-10-15T10:00:00Z"}` + "\n":                      "json",
		"2026-10-15T10:00:00Z INFO plain docker -t style line\n2026-10-15T10:00:01Z ERROR another\n": "generic",
		"2026-10-15 10:00:00 INFO ordinary application line\n":                                       "generic",
	} {
		if got := reg.Detect([]byte(sample)); got.Name != want {
			t.Errorf("sample %q detected as %q, want %q", strings.SplitN(sample, "\n", 2)[0], got.Name, want)
		}
	}
}
