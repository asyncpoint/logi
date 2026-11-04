package tui

import (
	"os"
	"strings"
	"testing"
)

// styledSpan reports whether needle, as it literally appears in the rendered
// screen, is opened by a real (non-reset) SGR sequence — i.e. actually
// highlighted, not just present as plain text.
func styledSpan(t *testing.T, raw, needle string) bool {
	t.Helper()
	i := strings.Index(raw, needle)
	if i < 0 {
		t.Fatalf("%q not present on screen:\n%s", needle, ansiStrip(raw))
	}
	loc := sgr.FindAllStringIndex(raw[:i], -1)
	if len(loc) == 0 || loc[len(loc)-1][1] != i {
		return false
	}
	p := sgrParams(raw[loc[len(loc)-1][0]:i])
	return p != "" && p != "0"
}

func realCacheFixture(t *testing.T) string {
	b, err := os.ReadFile("../../test/fixtures/sample-ncache-realfile.log")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Regression for the reported bug: searching matched records outside the
// message column (their raw text was always searched correctly) but the
// screen showed no highlight anywhere, making it look like search only
// covered the message text.
func TestSearchHighlightsTheColumnWhereTheMatchActuallyIs(t *testing.T) {
	r := newRig(t, realCacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(200, 24))

	// "ConnectionTable" is only ever in the visible "event" column
	// (ConnectionTable.Start / ConnectionTable.GetConnection), never in
	// message/level/time/logger. Forward search from the top lands on the
	// first occurrence.
	r.press("/")
	r.typ("ConnectionTable")
	r.settle("match", func() bool { return strings.Contains(r.screen(), "match at #") })
	if !styledSpan(t, r.raw(), "ConnectionTable") {
		t.Fatalf("event-column match must be highlighted:\n%s", r.screen())
	}

	// "ERROR" is only in the level column of that same record.
	r.press("esc")
	r.press("/")
	r.typ("ERROR")
	r.settle("match", func() bool { return strings.Contains(r.screen(), "match at #") })
	if !styledSpan(t, r.raw(), "ERROR") {
		t.Fatalf("level-column match must be highlighted:\n%s", r.screen())
	}

	// "demoCache" is only in the (visible) logger field column, never message.
	r.press("esc")
	r.press("/")
	r.typ("demoCache")
	r.settle("match", func() bool { return strings.Contains(r.screen(), "match at #") })
	if !styledSpan(t, r.raw(), "demoCache") {
		t.Fatalf("field-column (logger) match must be highlighted:\n%s", r.screen())
	}
}

// A match that exists only in a HIDDEN column (appdomain, hidden by default)
// must still be found — search covers the whole raw entry — but naturally has
// nothing to highlight on screen since that column isn't shown.
func TestSearchStillFindsMatchesInHiddenColumns(t *testing.T) {
	r := newRig(t, realCacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(200, 24))
	if strings.Contains(headerRow(r), "appdomain") {
		t.Fatal("appdomain should be hidden by default for this fixture")
	}
	r.press("/")
	r.typ("Alachisoft.NCache.CacheHost") // present in every row's hidden appdomain field only
	r.settle("match", func() bool { return strings.Contains(r.screen(), "match at #1") })
	// the term appears in the prompt line itself (it echoes what was typed);
	// what must NOT happen is it leaking into the row area above the prompt.
	rows := strings.Join(strings.Split(r.screen(), "\n")[:r.m.bodyHeight()+2], "\n")
	if strings.Contains(rows, "Alachisoft") {
		t.Fatalf("the hidden column's text should not leak into the row area:\n%s", r.screen())
	}
}

// Showing the previously-hidden column where a match lives must then highlight it.
func TestShowingTheMatchingColumnRevealsTheHighlight(t *testing.T) {
	r := newRig(t, realCacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(200, 24))
	r.press(":")
	r.typ("toggle column: appdomain")
	r.press("enter")
	if !strings.Contains(headerRow(r), "appdomain") {
		t.Fatalf("appdomain should now be visible:\n%s", r.screen())
	}
	r.press("/")
	r.typ("Alachisoft.NCache.CacheHost")
	r.settle("match", func() bool { return strings.Contains(r.screen(), "match at #") })
	if !styledSpan(t, r.raw(), "Alachisoft.NCache.CacheHost") {
		t.Fatalf("once visible, the appdomain column must highlight the match:\n%s", r.screen())
	}
}

// A coincidental digit match inside the millisecond part of the timestamp:
// functional search still finds it (whole raw entry), and now the time column
// itself highlights it too, since the displayed time also literally contains it.
func TestSearchHighlightsWithinTheTimeColumn(t *testing.T) {
	r := newRig(t, realCacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(200, 24))
	// The raw file writes milliseconds with a comma ("50,142"); the displayed
	// time column reformats that as a dot ("50.142"). Match() always finds the
	// record via the raw entry; searching the HH:MM:SS portion, which is
	// identical in both forms, must also highlight it in the displayed column.
	r.press("/")
	r.typ("33:50")
	r.settle("match", func() bool { return strings.Contains(r.screen(), "match at #") })
	if !styledSpan(t, r.raw(), "33:50") {
		t.Fatalf("time-column match must be highlighted:\n%s", r.screen())
	}
}

// Every visible column type keeps rendering correctly with no active search
// (no matcher yet, or an empty query) — the refactor must not have broken the
// no-highlight path for id/time/level/source/field/message.
func TestRenderRowWithoutSearchIsUnaffectedByTheHighlightRefactor(t *testing.T) {
	r := newRig(t, realCacheFixture(t), nil)
	r.indexed()
	r.m.Update(sizeMsg(200, 24))
	scr := r.screen()
	for _, want := range []string{"demoCache", "INFO", "CacheInfo", "06:33:50"} {
		if !strings.Contains(scr, want) {
			t.Errorf("row missing %q with no active search:\n%s", want, scr)
		}
	}
}
