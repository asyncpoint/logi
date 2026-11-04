package ncache

import (
	"os"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
	"github.com/asyncpoint/logi/internal/parser"
)

func readFixture(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../../test/fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func frames(t testing.TB, text string) (out []string) {
	f := framing.NewMultilineFramer(engine.DefaultLimits(), 0, isStart)
	f.SetSkip(isHeader)
	f.SetTerminator(isTerminator)
	f.Push([]byte(text), func(fr *framing.Frame) { out = append(out, string(fr.Data)) })
	f.Flush(true, func(fr *framing.Frame) { out = append(out, string(fr.Data)) })
	return
}

// ---- cache log: pure-space fixture ---------------------------------------

func TestCacheLogLayoutFromRealHeader(t *testing.T) {
	sample := readFixture(t, "sample-ncache-table.log")
	lay := layoutFromSample([]byte(sample), cacheLogLayout)
	want := []int{0, 32, 80, 128, 176, 192}
	if !intsEqual(lay.off, want) {
		t.Fatalf("offsets %v, want %v", lay.off, want)
	}
	if got := layoutFromSample([]byte("no header here\n"), cacheLogLayout); !intsEqual(got.off, cacheLogLayout.off) {
		t.Fatal("without a header the fallback layout applies")
	}
	// A header with no data rows yet to realign against: its own offsets stand.
	// (alignToRows only kicks in once real rows are available to vote with; see
	// TestCacheLogLayoutFromRealHeader's use of the actual fixture above, and
	// TestRealCacheLogFileWithTabsAndMixedLineEndings, for realignment against
	// genuine data.)
	headerOnly := "TIMESTAMP     APPDOMAIN   LOGGERNAME   THREADNAME   LEVEL   MESSAGE\n"
	if l := layoutFromSample([]byte(headerOnly), cacheLogLayout); !intsEqual(l.off, []int{0, 14, 26, 39, 52, 60}) {
		t.Fatalf("custom header-only widths must be honoured: %v", l.off)
	}
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCacheLogHeaderIsNotARecordAndRealLinesParse(t *testing.T) {
	sample := readFixture(t, "sample-ncache-table.log")
	recs := frames(t, sample)
	if strings.Contains(recs[0], "APPDOMAIN") {
		t.Fatalf("the column header must not become a record: %q", recs[0])
	}
	p := Parser{lay: layoutFromSample([]byte(sample), cacheLogLayout)}

	r, err := p.Parse(recs[0])
	if err != nil || r.Severity != engine.SevInfo || r.Timestamp.Format("15:04:05.000") != "06:23:20.825" {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Fields.GetValue("appdomain") != "Alachisoft.NCache.CacheHost" || r.Fields.GetValue("logger") != "demoCache" ||
		r.Fields.GetValue("thread") != ".NET TP Worker" || r.Fields.GetValue("event") != "CacheInfo" {
		t.Fatalf("fields: %v", r.Fields)
	}
	if !strings.HasPrefix(r.Message, "NCache Version : 5.3 SP7 ; Process Id : 261;") || strings.Contains(r.Message, "CacheInfo") {
		t.Fatalf("message must be the text after the event name: %q", r.Message)
	}
}

func TestCacheLogEveryLevelAndNumericThread(t *testing.T) {
	sample := readFixture(t, "sample-ncache-table.log")
	p := Parser{lay: layoutFromSample([]byte(sample), cacheLogLayout)}
	seen := map[engine.Severity]int{}
	var errRec engine.LogRecord
	for _, line := range frames(t, sample) {
		r, err := p.Parse(line)
		if err != nil || r.ParseErr {
			t.Fatalf("record failed to parse: %q (%v)", line, err)
		}
		seen[r.Severity]++
		if r.Severity == engine.SevError && r.Fields.GetValue("thread") == "43" {
			errRec = r
		}
	}
	for _, sev := range []engine.Severity{engine.SevDebug, engine.SevInfo, engine.SevWarn, engine.SevError, engine.SevFatal} {
		if seen[sev] == 0 {
			t.Errorf("no %v record parsed", sev)
		}
	}
	if seen[engine.SevUnknown] != 0 {
		t.Fatalf("no record may be left without a level: %v", seen)
	}
	if errRec.Fields.GetValue("event") != "ConnectionTable.GetConnection" || errRec.Message != "Connection refused 172.21.0.2:7803" {
		t.Fatalf("%+v", errRec)
	}
}

func TestCacheLogStackTraceFoldsIntoItsRecord(t *testing.T) {
	sample := readFixture(t, "sample-ncache-table.log")
	var trace string
	for _, r := range frames(t, sample) {
		if strings.Contains(r, "OperationFailedException") {
			trace = r
		}
	}
	if trace == "" || strings.Count(trace, "\n") != 3 {
		t.Fatalf("expected the ERROR line plus 3 continuation lines in one record: %q", trace)
	}
	r, _ := Parser{lay: layoutFromSample([]byte(sample), cacheLogLayout)}.Parse(trace)
	if r.Severity != engine.SevError || !r.Multiline || r.Fields.GetValue("exception") != "Alachisoft.NCache.Runtime.Exceptions.OperationFailedException" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Message, "Operation failed for key demo-77") || !strings.Contains(r.Message, "at Alachisoft.NCache.SocketServer.Command.InsertCommand.Execute()") {
		t.Fatalf("message must keep the trace: %q", r.Message)
	}
}

func TestRowsWithDifferentWidthsFallBackToGapSplitting(t *testing.T) {
	line := "2026-09-20 06:23:20,825  Alachisoft.NCache.CacheHost  demoCache  .NET TP Worker  WARN  Cache.Add   text with  two spaces"
	r, err := Parser{}.Parse(line)
	if err != nil || r.Severity != engine.SevWarn || r.Fields.GetValue("logger") != "demoCache" || r.Fields.GetValue("thread") != ".NET TP Worker" || r.Fields.GetValue("event") != "Cache.Add" {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Message != "text with  two spaces" {
		t.Fatalf("message text keeps its own spacing: %q", r.Message)
	}
	long := "2026-09-20 06:23:20,825         Alachisoft.NCache.CacheHost                     " + strings.Repeat("L", 60) +
		"  .NET TP Worker  ERROR  Evt  boom"
	if r, _ := (Parser{}).Parse(long); r.Severity != engine.SevError {
		t.Fatalf("overflowing column: %+v", r)
	}
}

func TestOlderFlatLayoutStillGetsALevel(t *testing.T) {
	r, err := Parser{}.Parse("2026-09-19 11:32:04,123  INFO   [Thread-4] Cache started")
	if err != nil || r.Severity != engine.SevInfo || r.Message != "Cache started" || r.Fields.GetValue("thread") != "Thread-4" {
		t.Fatalf("%+v %v", r, err)
	}
	if r, err := (Parser{}).Parse("garbage"); err == nil || !r.ParseErr || r.Message != "garbage" {
		t.Fatal("malformed entry must be flagged, not lost")
	}
}

// ---- cache log: the real uploaded file (tabs, mixed CRLF/LF, trailing END) ---

func TestRealCacheLogFileWithTabsAndMixedLineEndings(t *testing.T) {
	sample := readFixture(t, "sample-ncache-realfile.log")
	lay := layoutFromSample([]byte(sample), cacheLogLayout)
	recs := frames(t, sample)

	if strings.Contains(recs[0], "APPDOMAIN") {
		t.Fatalf("the tab-separated header must still be recognised and skipped: %q", recs[0])
	}
	p := Parser{lay: lay}
	var infoN, errN int
	for i, line := range recs {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r, err := p.Parse(line)
		if err != nil || r.ParseErr || r.Severity == engine.SevUnknown {
			t.Fatalf("record %d has no level despite tab-separated columns: %q (err=%v)", i, line, err)
		}
		switch r.Severity {
		case engine.SevInfo:
			infoN++
		case engine.SevError:
			errN++
			if r.Fields.GetValue("appdomain") == "" || r.Fields.GetValue("logger") == "" {
				t.Fatalf("ERROR record missing fields from tab-separated columns: %+v", r)
			}
		}
		if strings.Contains(r.Message, "\nEND") || strings.HasSuffix(strings.TrimRight(r.Message, "\r\n"), "END") {
			t.Fatalf("the trailing END marker must not leak into a record's message: %q", r.Message)
		}
	}
	if infoN != 60 || errN != 2 {
		t.Fatalf("info=%d error=%d, want 60 and 2", infoN, errN)
	}
}

func TestRealCacheLogFileDetectsAsNCache(t *testing.T) {
	sample := readFixture(t, "sample-ncache-realfile.log")
	if s := detectCache([]byte(sample)); s < 0.9 {
		t.Fatalf("real tab-separated cache log score %f", s)
	}
}

// ---- event log ------------------------------------------------------------

func TestEventLogLayoutFromRealHeader(t *testing.T) {
	sample := readFixture(t, "sample-ncache-events.log")
	lay := layoutFromSample([]byte(sample), eventLogLayout)
	if lay.levelIdx() < 0 || len(lay.names) != 5 || lay.names[1] != "component" {
		t.Fatalf("event log layout: %+v", lay)
	}
	if got := layoutFromSample([]byte("no header here\n"), eventLogLayout); got.names[1] != "component" {
		t.Fatal("without a header the fallback (event) layout applies")
	}
}

func TestEventLogParsesWindowsLevelsAndComponent(t *testing.T) {
	sample := readFixture(t, "sample-ncache-events.log")
	lay := layoutFromSample([]byte(sample), eventLogLayout)
	p := Parser{name: EventsName, lay: lay}
	recs := frames(t, sample)

	if strings.Contains(recs[0], "EVENTID") {
		t.Fatalf("the event log header must not become a record: %q", recs[0])
	}
	if len(recs) != 9 {
		t.Fatalf("got %d records, want 9 (header skipped, blank+END not counted as records)", len(recs))
	}
	first, err := p.Parse(recs[0])
	if err != nil || first.Severity != engine.SevInfo || first.Fields.GetValue("component") != "NCache" || first.Fields.GetValue("eventid") != "1020" {
		t.Fatalf("%+v %v", first, err)
	}
	if !strings.HasPrefix(first.Message, "NCache Server has been successfully registered") {
		t.Fatalf("message: %q", first.Message)
	}
	if strings.Contains(first.Message, "END") || strings.Contains(first.Message, "\n\n") {
		t.Fatalf("blank line / END leaked into the first record: %q", first.Message)
	}
	if first.Timestamp.Hour() != 6 || first.Timestamp.Minute() != 22 || first.Timestamp.Second() != 56 {
		t.Fatalf("timestamp with a space before the milliseconds: %v", first.Timestamp)
	}
	for _, line := range recs[1:] {
		r, err := p.Parse(line)
		if err != nil || r.ParseErr || r.Severity != engine.SevInfo {
			t.Fatalf("record failed to parse with its Windows event level: %q (%v) sev=%v", line, err, r.Severity)
		}
	}
}

func TestEventLogDetection(t *testing.T) {
	sample := readFixture(t, "sample-ncache-events.log")
	if s := detectEvents([]byte(sample)); s < 0.9 {
		t.Fatalf("event log score %f", s)
	}
	if s := detectCache([]byte(sample)); s >= 0.5 {
		t.Fatalf("the event log must not be claimed by the cache-log profile: %f", s)
	}
}

func TestWindowsEventLevelsAllMapCorrectly(t *testing.T) {
	for word, want := range map[string]engine.Severity{
		"Information": engine.SevInfo, "Warning": engine.SevWarn, "Error": engine.SevError,
		"Critical": engine.SevCritical, "Verbose": engine.SevTrace,
	} {
		if got := engine.ParseSeverity(word); got != want {
			t.Errorf("%s => %v, want %v", word, got, want)
		}
	}
}

// ---- shared: detection, registry, terminator -------------------------------

func TestDetectionRejectsGenericLogs(t *testing.T) {
	plain := "2026-09-19 11:32:04,123 INFO  something else entirely\n2026-09-19 11:32:05,123 WARN  more\n"
	if s := detectCache([]byte(plain)); s >= 0.5 {
		t.Fatalf("generic log4net-style output must not be claimed as a cache log: %f", s)
	}
	if s := detectEvents([]byte(plain)); s >= 0.5 {
		t.Fatalf("generic log4net-style output must not be claimed as an event log: %f", s)
	}
	if detectCache([]byte("hello\nworld\n")) != 0 || detectEvents([]byte("hello\nworld\n")) != 0 {
		t.Fatal("non-matching")
	}
}

func TestBothProfilesRegisterAndDeclareTheirOwnColumns(t *testing.T) {
	r := parser.NewRegistry()
	if err := parser.RegisterCore(r); err != nil {
		t.Fatal(err)
	}
	if err := New().Register(r); err != nil {
		t.Fatal(err)
	}

	cacheSample := readFixture(t, "sample-ncache-table.log")
	prof := r.Detect([]byte(cacheSample))
	if prof.Name != Name {
		t.Fatalf("cache log should select %q, got %q", Name, prof.Name)
	}
	var names []string
	for _, c := range prof.ColumnLayout() {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "time,appdomain,logger,thread,level,event,source,message" {
		t.Fatalf("cache column order: %s", got)
	}
	if !prof.ColumnLayout()[1].Hidden || prof.ColumnLayout()[2].Hidden {
		t.Fatal("appdomain starts hidden, logger visible")
	}

	eventSample := readFixture(t, "sample-ncache-events.log")
	eprof := r.Detect([]byte(eventSample))
	if eprof.Name != EventsName {
		t.Fatalf("event log should select %q, got %q", EventsName, eprof.Name)
	}
	names = nil
	for _, c := range eprof.ColumnLayout() {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "time,component,eventid,level,source,message" {
		t.Fatalf("event column order: %s", got)
	}
	if names[1] == "source" {
		t.Fatal("the event log's own SOURCE field must not collide with the built-in source column")
	}
}

func TestTerminatorClosesTheLastRecordCleanly(t *testing.T) {
	for _, name := range []string{"sample-ncache-events.log", "sample-ncache-realfile.log"} {
		t.Run(name, func(t *testing.T) {
			sample := readFixture(t, name)
			recs := frames(t, sample)
			last := recs[len(recs)-1]
			if strings.TrimRight(last, "\r\n") == "" {
				t.Fatal("terminator must not produce an empty trailing record")
			}
			if strings.Contains(last, "END") {
				t.Fatalf("END must be consumed, not folded into the record it closes: %q", last)
			}
		})
	}
}

func BenchmarkParseColumnarCache(b *testing.B) {
	line := "2026-09-20 06:23:20,979         Alachisoft.NCache.CacheHost                     demoCache                                       43                                              ERROR           ConnectionTable.GetConnection           Connection refused 172.21.0.2:7803"
	p := Parser{lay: cacheLogLayout}
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.Parse(line)
	}
}

func BenchmarkParseColumnarEvent(b *testing.B) {
	line := "2026-09-20 06:22:56, 110                        NCache                  1020                    Information             NCache Server has been successfully registered for FREE evaluation on machine 172.21.0.2."
	p := Parser{name: EventsName, lay: eventLogLayout}
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.Parse(line)
	}
}

func BenchmarkParseGapFallback(b *testing.B) {
	line := "2026-09-20 06:23:20,825  Alachisoft.NCache.CacheHost  demoCache  .NET TP Worker  WARN  Cache.Add   text with  two spaces"
	p := Parser{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.Parse(line)
	}
}

// The event name is the one NCache field worth a column when its logs are mixed
// with other formats (parser.MixedColumns picks columns marked Mixed).
func TestCacheEventColumnIsShownWhenMixed(t *testing.T) {
	r := parser.NewRegistry()
	if err := (&Plugin{}).Register(r); err != nil {
		t.Fatal(err)
	}
	p, ok := r.Get(Name)
	if !ok {
		t.Fatal("cache profile not registered")
	}
	cols := parser.MixedColumns([]*parser.Profile{p})
	var have []string
	for _, c := range cols {
		have = append(have, c.Name)
	}
	if want := []string{"time", "level", "source", "event", "message"}; !intsEqualStr(have, want) {
		t.Fatalf("mixed layout = %v, want %v", have, want)
	}
}

func intsEqualStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
