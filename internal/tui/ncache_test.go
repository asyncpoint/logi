package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
)

func ncacheFixture(t *testing.T) string {
	b, err := os.ReadFile("../../test/fixtures/sample-ncache-table.log")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The exact scenario from the bug report: a real NCache cache log.
func TestRealNCacheLogShowsItsOwnColumnsAndLevels(t *testing.T) {
	r := newRig(t, ncacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(160, 24))
	scr := r.screen()
	lines := strings.Split(scr, "\n")

	// 1. the header is the file's columns in the file's order (appdomain starts hidden)
	h := lines[1]
	for _, want := range []string{"time", "logger", "thread", "level", "event", "message"} {
		if !strings.Contains(h, want) {
			t.Errorf("column header missing %q: %q", want, h)
		}
	}
	order := []string{"time", "logger", "thread", "level", "event", "message"}
	last := -1
	for _, c := range order {
		at := strings.Index(h, c)
		if at <= last {
			t.Fatalf("columns must follow the file header order: %q", h)
		}
		last = at
	}
	if strings.Contains(h, "appdomain") {
		t.Errorf("appdomain is hidden by default: %q", h)
	}

	// 2. the file's own header line is NOT a record: the first row is the first log line
	if strings.Contains(scr, "TIMESTAMP") || strings.Contains(scr, "APPDOMAIN") || strings.Contains(scr, "LOGGERNAME") {
		t.Fatalf("the file header must not appear as a record:\n%s", scr)
	}
	first := lines[2]
	for _, want := range []string{"06:23:20.825", "demoCache", ".NET TP Worker", "INFO", "CacheInfo", "NCache Version : 5.3 SP7"} {
		if !strings.Contains(first, want) {
			t.Errorf("first row missing %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "Alachisoft.NCache.CacheHost") {
		t.Errorf("appdomain must not leak into the message: %q", first)
	}
	if r.s.ViewLen() != 10 {
		t.Fatalf("expected 10 records (header skipped, stack trace folded), got %d", r.s.ViewLen())
	}

	// 3. the level column is filled on every row, including the ERROR line with thread "43"
	var errRow string
	for _, l := range lines[2:] {
		if strings.Contains(l, "ConnectionTable.GetConnection") {
			errRow = l
		}
	}
	if !strings.Contains(errRow, "ERROR") || !strings.Contains(errRow, "43") || !strings.Contains(errRow, "Connection refused 172.21.0.2:7803") {
		t.Fatalf("ERROR row: %q", errRow)
	}
	for row := 0; row < r.s.ViewLen(); row++ {
		if rec, _ := r.s.ViewAt(row); rec.Severity == engine.SevUnknown {
			t.Errorf("record %d has no level: %q", rec.ID, rec.Message)
		}
	}
}

func TestRealNCacheLevelsAreColoured(t *testing.T) {
	r := newRig(t, ncacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(160, 24))
	r.press("G") // keep the cursor off the rows we inspect (the cursor row is drawn in reverse video)
	r.press("g")
	r.press("j", "j", "j", "j", "j", "j", "j", "j", "j") // last row
	raw := r.raw()
	styled := func(needle string) (params string, ok bool) {
		i := strings.Index(raw, needle)
		if i < 0 {
			t.Fatalf("%q not on screen", needle)
		}
		loc := sgr.FindAllStringIndex(raw[:i], -1)
		if len(loc) == 0 || loc[len(loc)-1][1] != i {
			return "", false
		}
		p := sgrParams(raw[loc[len(loc)-1][0]:i])
		return p, p != "" && p != "0"
	}
	// ERROR / WARN rows are coloured on the label AND on the event and message text
	for _, c := range []struct{ needle, what string }{
		{"ERROR", "ERROR label"}, {"ConnectionTable.GetConnection", "ERROR event"}, {"Connection refused", "ERROR message"},
		{"WARN", "WARN label"}, {"Item expired before insertion", "WARN message"},
	} {
		if _, ok := styled(c.needle); !ok {
			t.Errorf("%s is not coloured", c.what)
		}
	}
	errP, _ := styled("ERROR")
	warnP, _ := styled("WARN")
	infoP, _ := styled("INFO")
	if errP == warnP || errP == infoP || warnP == infoP {
		t.Errorf("ERROR, WARN and INFO must use different colours: %q %q %q", errP, warnP, infoP)
	}
	// an INFO message is plain text
	if _, ok := styled("NCache Version"); ok {
		t.Error("INFO message text should stay uncoloured")
	}
}

func TestNCacheColumnsCanBeHiddenAndShown(t *testing.T) {
	r := newRig(t, ncacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(160, 24))
	// show the app domain column through the palette
	r.press(":")
	r.typ("toggle column: appdomain")
	r.press("enter")
	if !strings.Contains(headerRow(r), "appdomain") || !strings.Contains(firstDataRow(r), "Alachisoft.NCache.CacheHost") {
		t.Fatalf("appdomain column should now show:\n%s", r.screen())
	}
	// select the thread column with h/l and hide it with x
	for i := 0; i < 12 && r.m.curName() != "thread"; i++ {
		r.press("l")
	}
	if r.m.curName() != "thread" {
		t.Fatalf("could not reach the thread column, at %q", r.m.curName())
	}
	r.press("x")
	if strings.Contains(headerRow(r), "thread") || strings.Contains(firstDataRow(r), ".NET TP Worker") {
		t.Fatalf("thread column must be hidden:\n%s", r.screen())
	}
	// filtering by a column's field works with key=value
	r.press("X")
	r.press("f")
	r.typ("thread=43")
	r.press("enter")
	r.settle("filter", func() bool { st := r.s.Status(); return !st.Filtering && st.ViewRows == 1 })
	if !strings.Contains(r.screen(), "Connection refused") {
		t.Fatalf("thread=43 should leave the ERROR record:\n%s", r.screen())
	}
}

func TestConfigHidesFormatSpecificColumns(t *testing.T) {
	r := newRig(t, ncacheFixture(t), func(c *config.Config) {
		c.HideColumns = []string{"thread", "id"}
		c.ShowColumns = []string{"appdomain"}
	})
	r.indexed()
	r.m.Update(sizeMsg(170, 24))
	h := headerRow(r)
	if strings.Contains(h, "thread") || strings.Contains(h, "#") || !strings.Contains(h, "appdomain") {
		t.Fatalf("config layout not applied: %q", h)
	}
	// a name the format does not have is reported, not silently ignored
	r2 := newRig(t, ncacheFixture(t), func(c *config.Config) { c.HideColumns = []string{"nosuchcolumn"} })
	r2.indexed()
	if !strings.Contains(r2.screen(), `no such column "nosuchcolumn"`) {
		t.Fatalf("unknown column must be reported:\n%s", r2.screen())
	}
}

// Live mode only learns the format when data arrives; the columns must follow.
func TestRefreshLayoutRepairsStaleColumnsForKnownProfile(t *testing.T) {
	r := newRig(t, ncacheFixture(t), func(c *config.Config) { c.Parser = "ncache" })
	r.indexed()
	// Simulate a profile name already published while the model still carries
	// the generic column set. The layout comparison must repair this mismatch
	// even when the profile name itself has not changed.
	r.m.cols = []colSpec{
		{name: "id", title: "#", kind: kID},
		{name: "time", title: "time", kind: kTime},
		{name: "level", title: "level", kind: kLevel},
		{name: "message", title: "message", kind: kMessage},
	}
	r.m.visibleColsCache = nil
	r.m.layoutName = r.s.ProfileName()
	r.m.refreshLayout()
	if !strings.Contains(headerRow(r), "logger") || !strings.Contains(headerRow(r), "event") {
		t.Fatalf("stale columns were not repaired for profile %q: %q", r.s.ProfileName(), headerRow(r))
	}
}

func TestLiveModeAdoptsTheFormatsColumnsWhenDetected(t *testing.T) {
	r := newRig(t, "", func(c *config.Config) { c.Follow = true })
	if strings.Contains(headerRow(r), "logger") {
		t.Fatalf("before any data the default columns apply: %q", headerRow(r))
	}
	f, _ := os.OpenFile(r.s.Status().Path, os.O_APPEND|os.O_WRONLY, 0o644)
	defer f.Close()
	f.WriteString(ncacheFixture(t))
	r.settle("ncache detected and columns rebuilt", func() bool {
		return strings.Contains(headerRow(r), "logger") && strings.Contains(headerRow(r), "event") && r.s.Status().Records >= 10
	})
	if r.s.ProfileName() != "ncache" {
		t.Fatalf("parser %q", r.s.ProfileName())
	}
	if strings.Contains(r.screen(), "TIMESTAMP") {
		t.Fatalf("header must not be shown as a record in live mode either:\n%s", r.screen())
	}
	// the user's own choice survives later layout changes
	r.press("l", "l")
	r.press("x")
	hidden := r.m.curName()
	_ = hidden
	if r.m.hiddenByUser() != 1 {
		t.Fatalf("hidden by user = %d", r.m.hiddenByUser())
	}
}

// Started mid-file (backlog), the header at the top of the file must still be honoured.
func TestLiveBacklogStillFindsTheHeaderAtTheTopOfTheFile(t *testing.T) {
	var b strings.Builder
	b.WriteString(strings.Split(ncacheFixture(t), "\n")[0] + "\n") // header
	pad := "2026-09-20 06:23:20,825         Alachisoft.NCache.CacheHost                     demoCache                                       .NET TP Worker                                  INFO            Filler                                  padding line to push the tail far from the top\n"
	for i := 0; i < 400; i++ {
		b.WriteString(pad)
	}
	b.WriteString("2026-09-20 06:23:22,410         Alachisoft.NCache.CacheHost                     demoCache                                       57                                              ERROR           Cache.Insert                            tail record with a level\n")
	r := newRig(t, b.String(), func(c *config.Config) { c.Follow, c.Backlog = true, 2048 })
	r.settle("tail record", func() bool { return r.s.Status().Records > 0 })
	r.settle("last record", func() bool {
		rec, ok := r.s.ViewAt(r.s.ViewLen() - 1)
		return ok && strings.Contains(rec.Message, "tail record")
	})
	rec, _ := r.s.ViewAt(r.s.ViewLen() - 1)
	if rec.Severity != engine.SevError || rec.Fields.GetValue("thread") != "57" || rec.Fields.GetValue("event") != "Cache.Insert" {
		t.Fatalf("mid-file start must still parse columns: %+v", rec)
	}
}

func TestNCacheDetailUsesFieldTable(t *testing.T) {
	r := newRig(t, ncacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(160, 30))
	r.press("enter")
	scr := r.screen()
	for _, want := range []string{"FIELDS", "time", "logger", "thread", "level", "event", "message", "demoCache", ".NET TP Worker", "CacheInfo"} {
		if !strings.Contains(scr, want) {
			t.Errorf("detail table missing %q:\n%s", want, scr)
		}
	}
	if strings.Contains(scr, "TIMESTAMP") || strings.Contains(scr, "APPDOMAIN") {
		t.Fatalf("detail must not expose the raw NCache header:\n%s", scr)
	}
}
