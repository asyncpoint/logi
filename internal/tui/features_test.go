package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func (r *rig) raw() string { return r.m.View().Content }

// ---- colours ------------------------------------------------------------------

func TestEveryLevelHasItsOwnColour(t *testing.T) {
	seen := map[string]engine.Severity{}
	for _, sev := range engine.AllSeverities() {
		lbl := themeFor(sev).label.Render("X")
		if !sgr.MatchString(lbl) {
			t.Errorf("%v label has no styling: %q", sev, lbl)
		}
		if other, dup := seen[lbl]; dup {
			t.Errorf("%v and %v share the same label style", sev, other)
		}
		seen[lbl] = sev
	}
	// message text: dim for trace/debug, coloured for warn and above, plain for info/notice/unknown
	for sev, wantStyled := range map[engine.Severity]bool{
		engine.SevUnknown: false, engine.SevTrace: true, engine.SevDebug: true, engine.SevInfo: false,
		engine.SevNotice: false, engine.SevWarn: true, engine.SevError: true, engine.SevCritical: true, engine.SevFatal: true,
	} {
		if got := sgr.MatchString(themeFor(sev).msg.Render("x")); got != wantStyled {
			t.Errorf("%v message styled=%v, want %v", sev, got, wantStyled)
		}
	}
	// warn / error / critical / fatal are different colours from each other
	seenMsg := map[string]bool{}
	for _, sev := range []engine.Severity{engine.SevWarn, engine.SevError, engine.SevCritical, engine.SevFatal} {
		seenMsg[themeFor(sev).msg.Render("x")] = true
	}
	if len(seenMsg) != 4 {
		t.Error("warn/error/critical/fatal messages must be visually distinct")
	}
}

func TestWholeRowIsColouredBySeverity(t *testing.T) {
	var b strings.Builder
	for i, sev := range []string{"TRACE", "DEBUG", "INFO", "NOTICE", "WARN", "ERROR", "CRITICAL", "FATAL"} {
		b.WriteString("2026-09-19 10:00:0" + string(rune('0'+i)) + " " + sev + " body-" + strings.ToLower(sev) + "\n")
	}
	r := newRig(t, b.String(), nil)
	r.indexed()
	r.press("G") // cursor on the last row so no other row is drawn in reverse video
	raw := r.raw()
	sgrParams := regexp.MustCompile(`\x1b\[([0-9;:]*)m`)
	styledBefore := func(body string) bool { // is the text opened by a real (non-reset) SGR sequence?
		i := strings.Index(raw, body)
		if i < 0 {
			t.Fatalf("%q not on screen", body)
		}
		loc := sgr.FindAllStringIndex(raw[:i], -1)
		if len(loc) == 0 || loc[len(loc)-1][1] != i {
			return false
		}
		params := sgrParams.FindStringSubmatch(raw[loc[len(loc)-1][0]:i])[1]
		return params != "" && params != "0" // "" / "0" is a reset: the text is unstyled
	}
	for body, want := range map[string]bool{
		"body-trace": true, "body-debug": true, "body-info": false, "body-notice": false,
		"body-warn": true, "body-error": true, "body-critical": true,
	} {
		if got := styledBefore(body); got != want {
			t.Errorf("%s: message coloured=%v, want %v", body, got, want)
		}
	}
	// every level label appears, each in its own style
	for _, lbl := range []string{"TRACE", "DEBUG", "INFO", "NOTE", "WARN", "ERROR", "CRIT", "FATAL"} {
		if !strings.Contains(ansiStrip(raw), lbl) {
			t.Errorf("label %s missing", lbl)
		}
	}
}

func ansiStrip(s string) string { return sgr.ReplaceAllString(s, "") }

func TestLevelNamesFromRealLogFormatsAllRender(t *testing.T) {
	content := strings.Join([]string{
		"2026-09-19 10:00:00 [VRB] serilog verbose",
		"2026-09-19 10:00:01 [dbug] mel debug",
		"2026-09-19 10:00:02 [INF] serilog info",
		"2026-09-19 10:00:03 SUCCESS loguru success",
		"2026-09-19 10:00:04 wrn short warn",
		"2026-09-19 10:00:05 fail mel error",
		"2026-09-19 10:00:06 ALERT syslog alert",
		"2026-09-19 10:00:07 [FTL] serilog fatal",
		"2026-09-19 10:00:08 no level here at all",
	}, "\n") + "\n"
	r := newRig(t, content, func(c *config.Config) { c.Parser = "generic" })
	r.indexed()
	want := []engine.Severity{engine.SevTrace, engine.SevDebug, engine.SevInfo, engine.SevNotice, engine.SevWarn,
		engine.SevError, engine.SevCritical, engine.SevFatal, engine.SevUnknown}
	for i, w := range want {
		rec, ok := r.s.ViewAt(i)
		if !ok || rec.Severity != w {
			t.Errorf("row %d: severity %v, want %v (%q)", i, rec.Severity, w, rec.Message)
		}
	}
	// severity filters accept every alias
	r.press("f")
	r.typ("severity:>=critical")
	r.press("enter")
	r.settle("filter", func() bool { st := r.s.Status(); return !st.Filtering && st.ViewRows == 2 })
}

// glog/klog files consist solely of "Lmmdd hh:mm:ss.uuuuuu ..." lines.
func TestGlogFileColoursByLetterLevel(t *testing.T) {
	content := "I0920 11:32:04.000001 1 a.go:1] started\nW0920 11:32:05.000001 1 a.go:2] slow\nE0920 11:32:06.000001 1 a.go:3] failed\nF0920 11:32:07.000001 1 a.go:4] dead\n"
	r := newRig(t, content, func(c *config.Config) { c.Parser = "generic" })
	r.indexed()
	for i, w := range []engine.Severity{engine.SevInfo, engine.SevWarn, engine.SevError, engine.SevFatal} {
		if rec, _ := r.s.ViewAt(i); rec.Severity != w || rec.Timestamp.IsZero() {
			t.Errorf("glog row %d: %v ts=%v", i, rec.Severity, rec.Timestamp)
		}
	}
	if !strings.Contains(r.screen(), "FATAL") || !strings.Contains(r.screen(), "dead") {
		t.Fatalf("%s", r.screen())
	}
}

func TestHelpShowsLevelLegend(t *testing.T) {
	r := newRig(t, logText(3), nil)
	// Inspect the complete help body directly so this test validates the
	// legend content rather than depending on the overlay's viewport height
	// or keyboard-scroll behavior.
	scr := sgr.ReplaceAllString(strings.Join(r.m.helpBody(100), "\n"), "")
	for _, want := range []string{"Log levels", "TRACE", "DEBUG", "INFO", "NOTE", "WARN", "ERROR", "CRIT", "FATAL", "EXCEPTION"} {
		if !strings.Contains(scr, want) {
			t.Errorf("legend missing %q:\n%s", want, scr)
		}
	}
}

func TestStatusBarShowsColouredLevelCounters(t *testing.T) {
	r := newRig(t, "", func(c *config.Config) { c.Follow, c.Parser = true, "generic" })
	f, _ := os.OpenFile(r.s.Status().Path, os.O_APPEND|os.O_WRONLY, 0o644)
	defer f.Close()
	f.WriteString("2026-09-19 10:00:00 ERROR a\n2026-09-19 10:00:01 ERROR b\n2026-09-19 10:00:02 WARN c\n2026-09-19 10:00:03 CRITICAL d\n2026-09-19 10:00:04 FATAL e\n")
	r.settle("counts", func() bool { return r.s.Status().Records == 5 })
	scr := r.screen()
	for _, want := range []string{"C:2", "E:2", "W:1"} {
		if !strings.Contains(scr, want) {
			t.Errorf("status missing %s:\n%s", want, scr)
		}
	}
	if !sgr.MatchString(r.raw()) {
		t.Fatal("counters must be styled")
	}
}

func TestShortcutHintsLiveInStatusBar(t *testing.T) {
	r := newRig(t, logText(10), nil)
	r.m.Update(tea.WindowSizeMsg{Width: 140, Height: 20}) // wide enough for every hint beside the counters
	r.indexed()
	lines := strings.Split(r.raw(), "\n")
	if len(lines) == 0 {
		t.Fatal("empty screen")
	}
	status := lines[len(lines)-1]
	for _, want := range []string{"? help", "/ search", "f filter", "h/l col", "x hide", "enter inspect", ": cmds", "q quit"} {
		if !strings.Contains(status, want) {
			t.Errorf("status bar missing %q: %q", want, status)
		}
	}
	for _, line := range lines[:len(lines)-1] {
		if strings.Contains(line, "? help") || strings.Contains(line, "/ search") {
			t.Fatalf("shortcut hints should not occupy a separate line: %q", line)
		}
	}
}

func TestDetailHeaderDoesNotLeakANSISequences(t *testing.T) {
	r := newRig(t, "2026-09-20 06:33:57.301 INFO State transfer has ended\n", func(c *config.Config) { c.Parser = "generic" })
	r.indexed()
	r.press("enter")
	raw := r.raw()
	// Styled output legitimately contains ESC-prefixed SGR sequences. Only
	// fail if the bracketed fragments appear without their ESC introducer.
	ansiFragment := regexp.MustCompile(`(^|[^\x1b])\[([0-9;:]*)m`)
	if ansiFragment.MatchString(raw) {
		t.Fatalf("detail header leaked ANSI sequence fragments:\n%q", raw)
	}
	if !sgr.MatchString(raw) || !strings.Contains(ansiStrip(raw), "INFO") {
		t.Fatalf("detail header lost severity styling:\n%q", raw)
	}
}

// ---- columns -------------------------------------------------------------------

func headerRow(r *rig) string { return strings.Split(r.screen(), "\n")[1] }
func firstDataRow(r *rig) string {
	return strings.Split(r.screen(), "\n")[2]
}

func TestColumnsDefaultAndHideCurrent(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	h := headerRow(r)
	for _, want := range []string{"#", "time", "level", "message"} {
		if !strings.Contains(h, want) {
			t.Errorf("header missing %q: %q", want, h)
		}
	}
	if strings.Contains(h, "source") {
		t.Fatalf("source column is hidden for a single file: %q", h)
	}
	if !strings.Contains(firstDataRow(r), "10:00:01.") && !strings.Contains(firstDataRow(r), "10:00:01") {
		t.Fatalf("timestamp expected in row: %q", firstDataRow(r))
	}
	if r.m.curName() != "id" {
		t.Fatalf("first visible column is current, got %v", r.m.curName())
	}
	r.press("l") // -> time
	if r.m.curName() != "time" {
		t.Fatalf("l selects next column, got %v", r.m.curName())
	}
	r.press("x") // hide time
	if !r.m.isHidden("time") || strings.Contains(headerRow(r), "time") {
		t.Fatalf("time column must be hidden:\n%s", r.screen())
	}
	if strings.Contains(firstDataRow(r), "10:00:0") {
		t.Fatalf("hidden column must not render in rows: %q", firstDataRow(r))
	}
	if r.m.curName() == "time" || !strings.Contains(headerRow(r), "1 hidden") {
		t.Fatalf("focus must move on and a hint must show:\n%s", r.screen())
	}
	r.press("X")
	if r.m.hiddenByUser() != 0 || !strings.Contains(headerRow(r), "time") {
		t.Fatalf("X restores every column:\n%s", r.screen())
	}
}

func TestHideEachColumnAndKeepOne(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	r.press("l", "l") // level
	r.press("x")
	if strings.Contains(firstDataRow(r), "INFO") || strings.Contains(headerRow(r), "level") {
		t.Fatalf("level column must disappear:\n%s", r.screen())
	}
	r.press("X", "x") // hide id (focus is on id after showing all? ensure some column hidden)
	// keep hiding until one is left
	for i := 0; i < 8 && len(r.m.visibleCols()) > 1; i++ {
		r.press("x")
	}
	if len(r.m.visibleCols()) != 1 {
		t.Fatalf("visible: %v", r.m.visibleCols())
	}
	r.press("x")
	if len(r.m.visibleCols()) != 1 || !strings.Contains(r.screen(), "at least one column") {
		t.Fatalf("the last column must stay visible:\n%s", r.screen())
	}
	// the list still renders records
	if !strings.Contains(r.screen(), "message") && !strings.Contains(firstDataRow(r), "10:00") && strings.TrimSpace(firstDataRow(r)) == "" {
		t.Fatalf("rows must still render:\n%s", r.screen())
	}
}

func TestColumnHidingKeepsCursorHighlightAndMessage(t *testing.T) {
	r := newRig(t, logText(10), nil)
	r.indexed()
	r.press("x", "x", "x") // hide id, then time, then level (focus follows)
	row := firstDataRow(r)
	if !strings.Contains(row, "message 1") || strings.Contains(row, "INFO") {
		t.Fatalf("only the message should remain:\n%s", r.screen())
	}
	if v := r.m.visibleCols(); len(v) != 1 || r.m.cols[v[0]].name != "message" {
		t.Fatalf("visible=%v", r.m.visibleCols())
	}
}

func TestToggleColumnFromPaletteAndConfig(t *testing.T) {
	r := newRig(t, logText(10), func(c *config.Config) { c.HideColumns = []string{"id"}; c.ShowColumns = []string{"source"} })
	r.indexed()
	h := headerRow(r)
	if strings.Contains(h, "#") || !strings.Contains(h, "source") {
		t.Fatalf("config must hide id and show source: %q", h)
	}
	if !strings.Contains(firstDataRow(r), "app.log") {
		t.Fatalf("source column shows the file name: %q", firstDataRow(r))
	}
	r.press(":")
	r.typ("toggle column: level")
	r.press("enter")
	if !r.m.isHidden("level") || strings.Contains(headerRow(r), "level") {
		t.Fatalf("palette toggle failed:\n%s", r.screen())
	}
	r.press(":")
	r.typ("toggle column: level")
	r.press("enter")
	if r.m.isHidden("level") {
		t.Fatal("toggle again shows it")
	}
}

func TestClickingHeaderSelectsColumn(t *testing.T) {
	r := newRig(t, logText(10), nil)
	r.indexed()
	x := strings.Index(headerRow(r), "level")
	r.m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: 1})
	if r.m.curName() != "level" {
		t.Fatalf("header click selected %v", r.m.curName())
	}
	r.press("x")
	if !r.m.isHidden("level") {
		t.Fatal("clicked column can be hidden")
	}
}

func TestColumnKeysAreRebindable(t *testing.T) {
	r := newRig(t, logText(10), func(c *config.Config) { c.Keys = map[string][]string{"hide_column": {"ctrl+x"}} })
	r.indexed()
	r.press("x") // no longer bound
	if r.m.isHidden("id") {
		t.Fatal("x should be unbound after rebinding")
	}
	r.press("ctrl+x")
	if !r.m.isHidden("id") {
		t.Fatal("ctrl+x should hide the current column")
	}
}

// ---- directories -----------------------------------------------------------------

func dirRig(t *testing.T, files map[string]string, mod func(*config.Config)) (*rig, string) {
	t.Helper()
	dir := t.TempDir()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // map order is random: age by sorted name so later names are newer
	for i, name := range names {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(files[name]), 0o644)
		tm := time.Now().Add(-time.Duration(len(names)-i) * time.Hour)
		os.Chtimes(p, tm, tm)
	}
	cfg := config.Defaults()
	cfg.Path, cfg.BatchInterval, cfg.Backlog = dir, 2*time.Millisecond, -1
	if mod != nil {
		mod(&cfg)
	}
	s, err := app.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel()
	s.Start(ctx)
	m := New(ctx, s, cfg)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	r := &rig{t, m, s}
	t.Cleanup(func() { cancel(); r.m.Session().Close() })
	return r, dir
}

func TestStaticDirectoryOpensPickerAndSwitchesFiles(t *testing.T) {
	r, _ := dirRig(t, map[string]string{
		"a-old.log": "2026-09-19 10:00:00 INFO from the old file\n",
		"b-new.log": "2026-09-20 10:00:00 ERROR from the new file\n",
	}, nil)
	if r.m.ov != ovPicker {
		t.Fatal("a static directory must open the picker")
	}
	scr := r.screen()
	for _, want := range []string{"Pick a file in", "a-old.log", "b-new.log", "●"} {
		if !strings.Contains(scr, want) {
			t.Errorf("picker missing %q:\n%s", want, scr)
		}
	}
	r.typ("old")
	list := strings.Join(strings.Split(r.screen(), "\n")[2:], "\n") // skip the title bar, which names the open file
	if strings.Contains(list, "b-new.log") || !strings.Contains(list, "a-old.log") {
		t.Fatalf("typing filters the list:\n%s", r.screen())
	}
	r.press("enter")
	r.settle("old file opened", func() bool { return strings.Contains(r.screen(), "from the old file") })
	if !strings.Contains(r.screen(), "a-old.log") {
		t.Fatalf("header should name the file:\n%s", r.screen())
	}
	r.press("ctrl+o") // picker is available again after choosing
	if r.m.ov != ovPicker {
		t.Fatal("ctrl+o must reopen the picker")
	}
	r.press("esc")
	if r.m.ov != ovNone {
		t.Fatal("esc closes the picker")
	}
}

func TestPickerOnPlainFileExplainsItself(t *testing.T) {
	r := newRig(t, logText(3), nil)
	r.indexed()
	r.press("ctrl+o")
	if r.m.ov != ovNone || !strings.Contains(r.screen(), "not browsing a directory") {
		t.Fatalf("%s", r.screen())
	}
}

func TestOpenPromptAcceptsADirectory(t *testing.T) {
	r := newRig(t, logText(3), nil)
	r.indexed()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.log"), []byte("2026-01-01 00:00:00 WARN in dir\n"), 0o644)
	r.press("ctrl+p")
	r.typ(dir)
	r.press("enter")
	if r.m.ov != ovPicker {
		t.Fatalf("opening a directory shows its picker: %s", r.screen())
	}
	r.press("enter")
	r.settle("file from dir", func() bool { return strings.Contains(r.screen(), "in dir") })
}

func TestLiveDirectoryHeaderAndSourceColumn(t *testing.T) {
	r, dir := dirRig(t, map[string]string{"day1.log": "2026-09-20 10:00:00 INFO day one\n"},
		func(c *config.Config) { c.Follow = true })
	r.settle("records", func() bool { return r.s.Status().Records == 1 })
	if !strings.Contains(r.screen(), filepath.Base(dir)+"/ → day1.log") {
		t.Fatalf("header should show directory and current file:\n%s", r.screen())
	}
	os.WriteFile(filepath.Join(dir, "day2.log"), []byte("2026-09-21 00:00:00 INFO day two\n"), 0o644)
	r.settle("rollover", func() bool { return strings.Contains(r.screen(), "→ day2.log") && r.s.Status().Records == 2 })

	r2, _ := dirRig(t, map[string]string{"a.log": "2026-09-20 10:00:00 INFO from a\n", "b.log": "2026-09-20 10:00:01 WARN from b\n"},
		func(c *config.Config) { c.Follow, c.DirMode = true, "all" })
	r2.settle("both", func() bool { return r2.s.Status().Records == 2 })
	scr := r2.screen()
	if !strings.Contains(scr, "(2 files)") || strings.Contains(headerRow(r2), "source") {
		t.Fatalf("all-mode hides the source column by default (the header names the cursor's file, the gutter shows the colour):\n%s", scr)
	}

	r3, _ := dirRig(t, map[string]string{"a.log": "2026-09-20 10:00:00 INFO from a\n", "b.log": "2026-09-20 10:00:01 WARN from b\n"},
		func(c *config.Config) { c.Follow, c.DirMode = true, "all"; c.ShowColumns = []string{"source"} })
	r3.settle("both", func() bool { return r3.s.Status().Records == 2 })
	scr3 := r3.screen()
	if !strings.Contains(headerRow(r3), "source") || !strings.Contains(scr3, "a.log") || !strings.Contains(scr3, "b.log") {
		t.Fatalf("--show-columns source brings the source column back:\n%s", scr3)
	}
}

func TestSourcePaletteIsDistinctAndAvoidsRed(t *testing.T) {
	seen := map[string]bool{}
	for _, code := range sourcePaletteCodes {
		if code == "1" || code == "9" {
			t.Errorf("palette uses red (%s), which means error elsewhere in the UI", code)
		}
		if seen[code] {
			t.Errorf("palette repeats colour %s", code)
		}
		seen[code] = true
	}
	rendered := map[string]int{}
	for slot := 0; slot < len(sourcePaletteCodes); slot++ {
		out := sourceStyleFor(slot).Render("x")
		if prev, dup := rendered[out]; dup {
			t.Errorf("slots %d and %d render identically: %q", prev, slot, out)
		}
		rendered[out] = slot
	}
	n := len(sourcePaletteCodes)
	if sourceStyleFor(n).Render("x") != sourceStyleFor(0).Render("x") || sourceStyleFor(-1).Render("x") != sourceStyleFor(n-1).Render("x") {
		t.Error("slots must wrap around the palette, including negative ones")
	}
}

func TestMultiFileSourceColumnColoursEachFile(t *testing.T) {
	r, _ := dirRig(t, map[string]string{
		"a.log": "2026-09-20 10:00:00 INFO from a\n",
		"b.log": "2026-09-20 10:00:01 INFO from b\n", // same severity: only the file can differ
	}, func(c *config.Config) { c.Follow, c.DirMode = true, "all"; c.ShowColumns = []string{"source"} })
	r.settle("both files", func() bool { return r.s.Status().Records == 2 })
	raw := r.raw()
	if !sgr.MatchString(raw) {
		t.Skip("renderer emitted no colour in this environment")
	}
	lead := func(name string) string {
		m := regexp.MustCompile(`((?:\x1b\[[0-9;:]*m)+)` + regexp.QuoteMeta(name)).FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("no styled %q on screen:\n%s", name, raw)
		}
		return m[1]
	}
	if a, b := lead("a.log"), lead("b.log"); a == b {
		t.Fatalf("two files share one style %q; each file needs its own colour", a)
	}
}

func TestSourceMarkerCells(t *testing.T) {
	n := len(sourcePaletteCodes)
	for slot := 0; slot < n; slot++ {
		if got := sourceMarkerCell(slot); got != "  " {
			t.Errorf("slot %d is in the first lap and must be unmarked, got %q", slot, got)
		}
	}
	for lap, want := range map[int]string{1: "◆ ", 2: "▲ ", 3: "■ ", 4: "● ", 5: "◆ "} {
		if got := sourceMarkerCell(lap*n + 3); got != want {
			t.Errorf("lap %d marker = %q, want %q", lap, got, want)
		}
	}
	for slot := 0; slot < 8*n; slot++ {
		if w := len([]rune(sourceMarkerCell(slot))); w != 2 {
			t.Errorf("slot %d marker has %d cells, want 2 so the column width never changes", slot, w)
		}
	}
}

func manyFiles(count int) map[string]string {
	files := map[string]string{}
	for i := 1; i <= count; i++ {
		files[fmt.Sprintf("f%02d.log", i)] = fmt.Sprintf("2026-09-20 10:00:%02d INFO line from file %d\n", i, i)
	}
	return files
}

func TestRepeatedColoursGetMarkersOnlyBeyondTheirPalette(t *testing.T) {
	r, _ := dirRig(t, manyFiles(12), func(c *config.Config) {
		c.Follow, c.DirMode, c.SourceMarkers = true, "all", true
		c.ShowColumns = []string{"source"}
	})
	r.settle("twelve files", func() bool { return r.s.Status().Records == 12 })
	screen := r.screen()
	for _, name := range []string{"f11.log", "f12.log"} {
		if !strings.Contains(screen, "◆ "+name) {
			t.Errorf("%s repeats a colour and should carry the second-lap marker:\n%s", name, screen)
		}
	}
	for _, name := range []string{"f01.log", "f10.log"} {
		if strings.Contains(screen, "◆ "+name) {
			t.Errorf("%s is in the first lap and must not be marked", name)
		}
	}
}

// Markers are opt-in: without --source-markers the column is unchanged however
// many files are followed.
func TestMarkersAreOffByDefault(t *testing.T) {
	r, _ := dirRig(t, manyFiles(12), func(c *config.Config) { c.Follow, c.DirMode = true, "all"; c.ShowColumns = []string{"source"} })
	r.settle("twelve files", func() bool { return r.s.Status().Records == 12 })
	if screen := r.screen(); strings.ContainsAny(screen, "◆▲■●") {
		t.Fatalf("no markers expected unless SourceMarkers is enabled:\n%s", screen)
	}
}

func TestNoMarkersWhenEveryFileHasItsOwnColour(t *testing.T) {
	r, _ := dirRig(t, manyFiles(10), func(c *config.Config) {
		c.Follow, c.DirMode, c.SourceMarkers = true, "all", true
		c.ShowColumns = []string{"source"}
	})
	r.settle("ten files", func() bool { return r.s.Status().Records == 10 })
	if screen := r.screen(); strings.ContainsAny(screen, "◆▲■●") {
		t.Fatalf("ten files fit the palette, so nothing should be marked:\n%s", screen)
	}
}

func TestHeaderNamesTheCursorFileInMultiFileView(t *testing.T) {
	first := "first-service-with-a-rather-long-name.log"
	second := "second-service-with-a-rather-long-name.log"
	r, _ := dirRig(t, map[string]string{
		first:  "2026-09-20 10:00:00 INFO from first\n",
		second: "2026-09-20 10:00:01 INFO from second\n",
	}, func(c *config.Config) { c.Follow, c.DirMode = true, "all" })
	r.settle("both files", func() bool { return r.s.Status().Records == 2 })
	r.press("G")
	rec, ok := r.s.Record(r.m.cursorID)
	if !ok {
		t.Fatal("no record under the cursor")
	}
	header := strings.SplitN(r.screen(), "\n", 2)[0]
	if !strings.Contains(header, rec.SourceID) {
		t.Fatalf("the header should name the cursor's file %q in full:\n%s", rec.SourceID, header)
	}
	r.press("g")
	rec2, _ := r.s.Record(r.m.cursorID)
	if rec2.SourceID == rec.SourceID {
		t.Fatalf("moving to another file's row should change the header name (still %q)", rec2.SourceID)
	}
	if h2 := strings.SplitN(r.screen(), "\n", 2)[0]; !strings.Contains(h2, rec2.SourceID) {
		t.Fatalf("header did not follow the cursor to %q:\n%s", rec2.SourceID, h2)
	}
}

func TestHeaderHasNoFileNameForASingleFile(t *testing.T) {
	r := newRig(t, "2026-09-20 10:00:00 INFO only line\n", nil)
	header := strings.SplitN(r.screen(), "\n", 2)[0]
	if strings.Count(header, ".log") > 1 {
		t.Fatalf("a single-file view already names its file in the title; header repeats it:\n%s", header)
	}
}

// In a mixed view (files of different formats) the generic layout has no column
// for NCache's event name; the mixed layout adds it in place of the source column.
func TestMixedDirectoryShowsTheEventColumnInsteadOfSource(t *testing.T) {
	fixture, err := os.ReadFile("../../test/fixtures/sample-ncache-table.log")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := dirRig(t, map[string]string{
		"cache.log": string(fixture),
		"plain.log": "2026-09-20 10:00:00 INFO a plain line\n",
	}, func(c *config.Config) { c.Follow, c.DirMode = true, "all" })
	r.settle("event column", func() bool { return strings.Contains(headerRow(r), "event") })
	if h := headerRow(r); strings.Contains(h, "source") {
		t.Fatalf("the source column stays hidden in the mixed layout: %q", h)
	}
	r.settle("event names", func() bool { return strings.Contains(r.screen(), "CacheHost.Stop") })
}

func TestGutterGlyph(t *testing.T) {
	n := len(sourcePaletteCodes)
	for _, c := range []struct {
		name                string
		slot                int
		bookmarked, markers bool
		want                string
	}{
		{"plain bar", 3, false, false, sourceGutterBar},
		{"bookmark wins", 3, true, false, "★"},
		{"repeat without markers keeps the bar", n + 2, false, false, sourceGutterBar},
		{"first lap with markers keeps the bar", n - 1, false, true, sourceGutterBar},
		{"second lap with markers", n + 2, false, true, "◆"},
		{"third lap with markers", 2*n + 2, false, true, "▲"},
		{"bookmark beats the marker but keeps its colour", n + 2, true, true, "★"},
	} {
		if got := gutterGlyph(c.slot, c.bookmarked, c.markers); got != c.want {
			t.Errorf("%s: gutterGlyph(%d, %v, %v) = %q, want %q", c.name, c.slot, c.bookmarked, c.markers, got, c.want)
		}
	}
}

// By default a multi-file view gives the source column's width to the message:
// the column is hidden and a colour bar in the gutter stands in for it.
func TestMultiFileViewHidesSourceColumnAndShowsGutterBar(t *testing.T) {
	files := map[string]string{
		"a.log": "2026-09-20 10:00:00 INFO from a\n",
		"b.log": "2026-09-20 10:00:01 INFO from b\n",
	}
	for _, c := range []struct {
		name       string
		gutter     string
		showSource bool
		wantBars   int
	}{
		{"default (auto, column hidden)", "auto", false, 2},
		{"off", "off", false, 0},
		{"column shown, auto: the column itself identifies files", "auto", true, 0},
		{"column shown, on: both", "on", true, 2},
	} {
		r, _ := dirRig(t, files, func(cfg *config.Config) {
			cfg.Follow, cfg.DirMode, cfg.SourceGutter = true, "all", c.gutter
			if c.showSource {
				cfg.ShowColumns = []string{"source"}
			}
		})
		r.settle("two files", func() bool { return r.s.Status().Records == 2 })
		screen := r.screen()
		if got := strings.Count(screen, "▌"); got != c.wantBars {
			t.Errorf("%s: %d gutter bars, want %d:\n%s", c.name, got, c.wantBars, screen)
		}
		if has := strings.Contains(headerRow(r), "source"); has != c.showSource {
			t.Errorf("%s: source column visible = %v, want %v:\n%s", c.name, has, c.showSource, screen)
		}
	}
}

func TestGutterKeepsBookmarkStar(t *testing.T) {
	r, _ := dirRig(t, map[string]string{
		"a.log": "2026-09-20 10:00:00 INFO from a\n",
		"b.log": "2026-09-20 10:00:01 INFO from b\n",
	}, func(c *config.Config) { c.Follow, c.DirMode = true, "all" })
	r.settle("two files", func() bool { return r.s.Status().Records == 2 })
	r.press("g") // cursor on the first row
	r.m.bookmarks[r.m.cursorID] = struct{}{}
	screen := r.screen()
	if !strings.Contains(screen, "★") {
		t.Fatalf("a bookmarked row must still show its star in the gutter:\n%s", screen)
	}
	if got := strings.Count(screen, "▌"); got != 1 {
		t.Fatalf("the starred row swaps its bar for the star, the other keeps its bar (want 1 bar), found %d:\n%s", got, screen)
	}
}

func TestGutterShowsRepeatMarkersWhenEnabled(t *testing.T) {
	r, _ := dirRig(t, manyFiles(12), func(c *config.Config) {
		c.Follow, c.DirMode, c.SourceMarkers = true, "all", true
	})
	r.settle("twelve files", func() bool { return r.s.Status().Records == 12 })
	screen := r.screen()
	if bars, shapes := strings.Count(screen, "▌"), strings.Count(screen, "◆"); bars != 10 || shapes != 2 {
		t.Fatalf("want 10 bars (first ten files) and 2 repeat shapes (files 11 and 12), got %d and %d:\n%s", bars, shapes, screen)
	}
}

func TestPositionNeverExceedsTotal(t *testing.T) {
	for _, c := range []struct {
		row, total int
		want       string
	}{{-1, 0, "0/0"}, {-1, 5, "0/0"}, {0, 1, "1/1"}, {2, 10, "3/10"}, {3668, 3649, "3,669/3,669"}, {0, 0, "1/1"}} {
		if got := positionText(c.row, c.total); got != c.want {
			t.Errorf("positionText(%d,%d) = %q, want %q", c.row, c.total, got, c.want)
		}
	}
}

func (m *Model) curName() string { return m.cols[m.curCol].name }

// Regression: a fixed-width help column breaks alignment (and visually
// crowds the next column) as soon as any entry is longer than that width —
// "pgdown / space / ctrl+d" (23 chars) against the old 22-char key column,
// and "Pick file from directory" (25 chars) against the old 20-char title
// column. Both columns must size themselves to their widest actual entry.
func TestHelpColumnsStayAlignedForLongEntries(t *testing.T) {
	r := newRig(t, logText(3), nil)
	r.indexed()
	r.m.Update(sizeMsg(120, 40)) // tall enough that the whole command list fits without scrolling
	r.press("?")
	scr := r.screen()

	lineWith := func(needle string) string {
		for _, l := range strings.Split(scr, "\n") {
			if strings.Contains(l, needle) {
				return l
			}
		}
		t.Fatalf("no help line contains %q:\n%s", needle, scr)
		return ""
	}

	// Title column: every row's title must start at the same offset,
	// including the row whose key combo is the longest.
	shortKeyRow := lineWith("down / j")
	longKeyRow := lineWith("pgdown / space / ctrl+d")
	titleOffShort := strings.Index(shortKeyRow, "Down")
	titleOffLong := strings.Index(longKeyRow, "Page down")
	if titleOffShort < 0 || titleOffLong < 0 {
		t.Fatalf("rows:\n%q\n%q", shortKeyRow, longKeyRow)
	}
	if titleOffShort != titleOffLong {
		t.Fatalf("title column misaligned: %d (short key) vs %d (long key)\n%q\n%q",
			titleOffShort, titleOffLong, shortKeyRow, longKeyRow)
	}
	// There must be visible separation, not just one collapsed space.
	if gap := titleOffLong - (strings.Index(longKeyRow, "pgdown") + len("pgdown / space / ctrl+d")); gap < 1 {
		t.Fatalf("no gap between the long key combo and its title: %q", longKeyRow)
	}

	// Description column: every row's description must start at the same
	// offset, including the row whose title is the longest.
	shortTitleRow := lineWith("Down ")
	longTitleRow := lineWith("Pick file from directory")
	descOffShort := strings.Index(shortTitleRow, "Move to the next record")
	descOffLong := strings.Index(longTitleRow, "List the directory's log files")
	if descOffShort < 0 || descOffLong < 0 {
		t.Fatalf("rows:\n%q\n%q", shortTitleRow, longTitleRow)
	}
	if descOffShort != descOffLong {
		t.Fatalf("description column misaligned: %d (short title) vs %d (long title)\n%q\n%q",
			descOffShort, descOffLong, shortTitleRow, longTitleRow)
	}
	if gap := descOffLong - strings.Index(longTitleRow, "Pick file from directory") - len("Pick file from directory"); gap < 1 {
		t.Fatalf("no gap between the long title and its description: %q", longTitleRow)
	}
}

func TestSelectionUsesCurrentViewOrderAndIndicatorsCanCoexist(t *testing.T) {
	content := strings.Join([]string{
		"2026-09-19 10:00:00 INFO first",
		"2026-09-19 10:00:01 ERROR second",
		"2026-09-19 10:00:02 WARN third",
	}, "\n")
	r := newRig(t, content, nil)
	r.indexed()
	first, _ := r.m.sess.IDAt(0)
	second, _ := r.m.sess.IDAt(1)
	third, _ := r.m.sess.IDAt(2)
	r.m.selectionStart, r.m.selectionEnd = third, first // reverse anchor order is valid
	if !r.m.selected(first) || !r.m.selected(second) || !r.m.selected(third) {
		t.Fatal("selection should span both endpoints in current view order")
	}
	r.m.bookmarks[second] = struct{}{}
	r.press("G")
	if !strings.Contains(ansiStrip(r.raw()), "★◆") {
		t.Fatalf("bookmark and selection indicators should both render:\n%s", ansiStrip(r.raw()))
	}
	if err := r.m.sess.SetFilter("severity:error"); err != nil {
		t.Fatal(err)
	}
	if r.m.selected(first) || r.m.selected(third) {
		t.Fatal("records outside the current filtered view must not be selected")
	}
}

func TestSearchResultIsIgnoredWhenSearchIsNoLongerPending(t *testing.T) {
	r := newRig(t, "2026-10-10 10:00:00 INFO alpha\n2026-10-10 10:00:01 ERROR beta\n", nil)
	r.indexed()
	r.m.sgen = 7
	r.m.sPending = false
	r.m.sstate = "newer search state"
	r.m.onSearchResult(&app.SearchResult{Gen: 7, Found: true, ID: 1, Query: "alpha"})
	if r.m.sstate != "newer search state" {
		t.Fatalf("stale result changed search state: %q", r.m.sstate)
	}
	if r.m.sfoundID != 0 {
		t.Fatalf("stale result selected record %d", r.m.sfoundID)
	}
}

func TestSearchResultDoesNotSelectRecordOutsideCurrentView(t *testing.T) {
	r := newRig(t, "2026-10-10 10:00:00 INFO alpha\n2026-10-10 10:00:01 ERROR beta\n", nil)
	r.indexed()
	if err := r.s.SetFilter("severity:error"); err != nil {
		t.Fatal(err)
	}
	r.settle("error filter", func() bool {
		return r.s.ViewLen() == 1 && r.s.Status().Filter == "severity:error" && !r.s.Status().Filtering
	})
	// ID 1 exists in storage but is excluded by the active view.
	r.m.sgen, r.m.sPending = 9, true
	r.m.onSearchResult(&app.SearchResult{Gen: 9, Found: true, ID: 1, Query: "alpha"})
	if r.m.sfoundID != 0 {
		t.Fatalf("hidden record was selected as a search result: %d", r.m.sfoundID)
	}
	if !strings.Contains(r.m.sstate, "no longer in the current view") {
		t.Fatalf("unexpected search status: %q", r.m.sstate)
	}
}

func TestExactViewRowRejectsNearestRowForMissingID(t *testing.T) {
	r := newRig(t, "2026-10-10 10:00:00 INFO alpha\n2026-10-10 10:00:01 ERROR beta\n", nil)
	r.indexed()
	if got := r.m.exactViewRow(999); got != -1 {
		t.Fatalf("exactViewRow(999) = %d, want -1", got)
	}
	if got := r.m.exactViewRow(1); got < 0 {
		t.Fatalf("exactViewRow(1) = %d, want visible row", got)
	}
}

// On a narrow terminal the status bar gives up whole hints, least useful first,
// and keeps "? help" and "q quit"; it never cuts a hint in half.
func TestShortcutHintsDropWholeWhenNarrow(t *testing.T) {
	r := newRig(t, logText(10), nil)
	r.indexed()
	for _, width := range []int{70, 80, 90, 100, 120} {
		r.m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		lines := strings.Split(r.raw(), "\n")
		status := engine.StripANSI(lines[len(lines)-1])
		for _, want := range []string{"? help", "q quit"} {
			if !strings.Contains(status, want) {
				t.Errorf("width %d: status bar lost %q: %q", width, want, status)
			}
		}
		if strings.Contains(status, "…") {
			t.Errorf("width %d: a hint was cut in half: %q", width, status)
		}
	}
	if fitHints(5) != "" {
		t.Error("with no room for the two essential hints, none should be shown")
	}
	if got := fitHints(1000); !strings.Contains(got, "h/l col") || !strings.HasSuffix(got, "q quit") {
		t.Errorf("with room to spare every hint is shown, got %q", got)
	}
	if got := fitHints(len("? help  q quit")); got != "? help  q quit" {
		t.Errorf("minimal fit = %q", got)
	}
}
