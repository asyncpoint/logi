package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
)

func logText(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		sev := "INFO"
		switch {
		case i%10 == 0:
			sev = "ERROR"
		case i%5 == 0:
			sev = "WARN"
		}
		fmt.Fprintf(&b, "2026-09-19 10:%02d:%02d %s message %d\n", (i/60)%60, i%60, sev, i)
	}
	return b.String()
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	if strings.HasPrefix(s, "ctrl+") {
		return tea.KeyPressMsg{Code: rune(s[5]), Mod: tea.ModCtrl}
	}
	if strings.HasPrefix(s, "alt+") {
		return tea.KeyPressMsg{Code: rune(s[4]), Mod: tea.ModAlt}
	}
	r, _ := utf8.DecodeRuneInString(s)
	return tea.KeyPressMsg{Code: r, Text: s}
}

type rig struct {
	t *testing.T
	m *Model
	s *app.Session
}

func newRig(t *testing.T, content string, mod func(*config.Config)) *rig {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(p, []byte(content), 0o644)
	cfg := config.Defaults()
	cfg.Path, cfg.BatchInterval, cfg.Backlog = p, 2*time.Millisecond, -1
	// These tests exercise the TUI and live-session behavior, not OS-specific
	// filesystem notification delivery. Polling keeps append/detection timing
	// deterministic under -race and on platforms where watcher events coalesce
	// or arrive late. The input package covers the watcher and polling paths.
	cfg.ForcePoll = true
	if mod != nil {
		mod(&cfg)
	}
	s, err := app.NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	m := New(ctx, s, cfg)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	r := &rig{t, m, s}
	t.Cleanup(func() { cancel(); r.m.Session().Close() })
	return r
}

// settle feeds session updates to the model until cond holds.
func (r *rig) settle(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		select {
		case u := <-r.m.Session().Updates():
			r.m.Update(updateMsg(u))
		case <-time.After(5 * time.Millisecond):
		}
	}
	r.t.Fatalf("timeout: %s\n%s", what, r.screen())
}

func (r *rig) press(keys ...string) {
	for _, k := range keys {
		r.m.Update(key(k))
	}
}

func (r *rig) typ(s string) {
	for _, c := range s {
		r.m.Update(key(string(c)))
	}
}

func (r *rig) screen() string { return ansi.Strip(r.m.View().Content) }

func (r *rig) indexed() {
	r.t.Helper()
	r.settle("index", func() bool { return r.m.Session().Status().IndexDone && r.m.cursorID != 0 })
}

func TestKeyStringsMatchBindings(t *testing.T) {
	for in, want := range map[string]string{"G": "G", "ctrl+f": "ctrl+f", "enter": "enter", "esc": "esc", "j": "j", "alt+r": "alt+r", "pgdown": "pgdown"} {
		if got := key(in).String(); got != want {
			t.Errorf("key(%q).String() = %q, want %q", in, got, want)
		}
	}
}

func TestInitialRenderStaticMode(t *testing.T) {
	r := newRig(t, logText(300), nil)
	r.indexed()
	scr := r.screen()
	for _, want := range []string{"STATIC", "app.log", "generic", "message 1", "1/300", "INFO"} {
		if !strings.Contains(scr, want) {
			t.Errorf("screen missing %q:\n%s", want, scr)
		}
	}
	lines := strings.Split(scr, "\n")
	if len(lines) != 20 {
		t.Fatalf("screen has %d lines, want 20", len(lines))
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > 100 {
			t.Errorf("line %d wider than terminal: %d", i, ansi.StringWidth(l))
		}
	}
}

func TestNavigation(t *testing.T) {
	r := newRig(t, logText(300), nil)
	r.indexed()
	r.press("j", "j")
	if r.m.cursorID != 3 {
		t.Fatalf("cursor %d", r.m.cursorID)
	}
	r.press("k")
	if r.m.cursorID != 2 {
		t.Fatalf("cursor %d", r.m.cursorID)
	}
	r.press("G")
	if r.m.cursorID != 300 || !strings.Contains(r.screen(), "message 300") {
		t.Fatalf("G: cursor %d\n%s", r.m.cursorID, r.screen())
	}
	r.press("g")
	if r.m.cursorID != 1 {
		t.Fatalf("g: %d", r.m.cursorID)
	}
	r.press("pgdown")
	if int(r.m.cursorID) != 1+r.m.bodyHeight()-1 {
		t.Fatalf("pgdown: %d", r.m.cursorID)
	}
	r.m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if int(r.m.cursorID) <= 1+r.m.bodyHeight()-1 {
		t.Fatal("wheel should scroll")
	}
}

func TestSearchLiveTypingHighlightAndRepeat(t *testing.T) {
	r := newRig(t, logText(300), nil)
	r.indexed()
	r.press("/")
	r.typ("message 4")
	r.settle("first match", func() bool { return r.m.cursorID == 4 })
	if !strings.Contains(r.screen(), "Search [literal]: message 4") {
		t.Fatalf("prompt missing:\n%s", r.screen())
	}
	r.press("enter")
	if r.m.ov != ovNone {
		t.Fatal("enter should close the prompt")
	}
	r.press("n")
	r.settle("next", func() bool { return r.m.cursorID == 40 })
	r.press("N")
	r.settle("prev", func() bool { return r.m.cursorID == 4 })
	// Esc during typing restores the original position.
	r.press("g")
	r.press("/")
	r.typ("message 200")
	r.settle("200", func() bool { return r.m.cursorID == 200 })
	r.press("esc")
	if r.m.cursorID != 1 {
		t.Fatalf("esc must restore position, at %d", r.m.cursorID)
	}
	// Invalid regex is reported, not fatal.
	r.press("/", "alt+r")
	r.typ("(")
	if !strings.Contains(r.screen(), "invalid") {
		t.Fatalf("regex error not shown:\n%s", r.screen())
	}
}

func TestFilterFlow(t *testing.T) {
	r := newRig(t, logText(300), nil)
	r.indexed()
	r.press("f")
	r.typ("severity:error")
	r.press("enter")
	r.settle("filtered", func() bool { st := r.s.Status(); return st.ViewRows == 30 && !st.Filtering })
	r.settle("cursor", func() bool { return r.m.cursorID != 0 })
	scr := r.screen()
	if !strings.Contains(scr, "filter: severity:error") || strings.Contains(scr, "INFO") {
		t.Fatalf("filter not applied:\n%s", scr)
	}
	r.press("c")
	r.settle("cleared", func() bool { return r.m.Session().ViewLen() == 300 })
	// bad filter keeps the prompt open with an error
	r.press("f")
	r.typ("severity:loud")
	r.press("enter")
	if r.m.ov != ovPrompt || !strings.Contains(r.screen(), "unknown severity") {
		t.Fatalf("bad filter must stay in prompt:\n%s", r.screen())
	}
}

func TestDetailOverlayPrettyPrintsJSON(t *testing.T) {
	content := `{"time":"2026-09-18T10:00:00Z","level":"error","msg":"disk <full>","request":{"id":"a1"}}` + "\n" +
		`{"time":"2026-09-18T10:00:01Z","level":"info","msg":"ok"}` + "\n"
	r := newRig(t, content, nil)
	r.indexed()
	if !strings.Contains(r.screen(), "json") || !strings.Contains(r.screen(), "disk <full>") {
		t.Fatalf("list should show parsed message:\n%s", r.screen())
	}
	r.press("enter")
	scr := r.screen()
	for _, want := range []string{"#1", `"level": "error"`, `"id": "a1"`, "disk <full>"} {
		if !strings.Contains(scr, want) {
			t.Errorf("detail missing %q:\n%s", want, scr)
		}
	}
	r.press("right")
	if !strings.Contains(r.screen(), "#2") {
		t.Fatalf("right arrow should show next record:\n%s", r.screen())
	}
	r.press("esc")
	if r.m.ov != ovNone {
		t.Fatal("esc closes detail")
	}
}

func TestMultilineRecordAndDetail(t *testing.T) {
	content := "2026-09-18 10:00:00 ERROR Request failed\nSystem.Exception: boom\n   at Foo.Bar()\n2026-09-18 10:00:01 INFO fine\n"
	r := newRig(t, content, nil)
	r.indexed()
	scr := r.screen()
	if !strings.Contains(scr, "Request failed ⏎+2") || strings.Contains(scr, "System.Exception") {
		t.Fatalf("list must fold continuation lines:\n%s", scr)
	}
	r.press("enter")
	if s := r.screen(); !strings.Contains(s, "System.Exception: boom") || !strings.Contains(s, "at Foo.Bar()") {
		t.Fatalf("detail must show the whole record:\n%s", s)
	}
}

func TestLiveFollowPauseResume(t *testing.T) {
	r := newRig(t, logText(5), func(c *config.Config) { c.Follow = true; c.Parser = "generic" })
	r.settle("initial", func() bool { return r.m.cursorID == 5 })
	if !strings.Contains(r.screen(), "LIVE") {
		t.Fatalf("live badge:\n%s", r.screen())
	}
	f, _ := os.OpenFile(r.s.Status().Path, os.O_APPEND|os.O_WRONLY, 0o644)
	defer f.Close()
	f.WriteString("2026-09-19 11:00:00 INFO appended one\n")
	r.settle("follow new", func() bool { return r.m.cursorID == 6 })
	r.press("up", "up")
	if r.m.follow || !strings.Contains(r.screen(), "paused") {
		t.Fatalf("moving up must pause following:\n%s", r.screen())
	}
	at := r.m.cursorID
	f.WriteString("2026-09-19 11:00:01 INFO appended two\n")
	r.settle("record arrived", func() bool { return r.s.Status().Records == 7 })
	if r.m.cursorID != at {
		t.Fatalf("paused view must not move: %d -> %d", at, r.m.cursorID)
	}
	r.press("G")
	if !r.m.follow || r.m.cursorID != 7 {
		t.Fatalf("G resumes following at the end: follow=%v cursor=%d", r.m.follow, r.m.cursorID)
	}
}

func TestPaletteRunsCommands(t *testing.T) {
	r := newRig(t, logText(20), nil)
	r.indexed()
	r.press(":")
	if r.m.ov != ovPalette || !strings.Contains(r.screen(), "Command:") {
		t.Fatalf("palette:\n%s", r.screen())
	}
	r.typ("help")
	r.press("enter")
	if r.m.ov != ovHelp || !strings.Contains(r.screen(), "Keys & commands") {
		t.Fatalf("help overlay:\n%s", r.screen())
	}
	for i := 0; i < 40 && !strings.Contains(r.screen(), "Filter syntax"); i++ {
		r.press("down") // help scrolls on small screens
	}
	if !strings.Contains(r.screen(), "Filter syntax") {
		t.Fatalf("help overlay:\n%s", r.screen())
	}
	r.press("esc")
	r.press(":")
	r.typ("go to bottom")
	r.press("enter")
	if r.m.cursorID != 20 {
		t.Fatalf("palette 'Go to bottom' -> cursor %d", r.m.cursorID)
	}
}

func TestOpenAnotherFile(t *testing.T) {
	r := newRig(t, logText(10), nil)
	r.indexed()
	other := filepath.Join(t.TempDir(), "other.log")
	os.WriteFile(other, []byte("2026-01-01 00:00:00 FATAL only record in the other file\n"), 0o644)
	r.press("ctrl+p")
	r.typ(filepath.Join(t.TempDir(), "nope.log"))
	r.press("enter")
	if r.m.ov != ovPrompt || r.m.perr == "" {
		t.Fatalf("missing file must keep the prompt open with an error:\n%s", r.screen())
	}
	r.press("ctrl+u")
	r.typ(other)
	r.press("enter")
	r.settle("new file", func() bool { return strings.Contains(r.screen(), "only record in the other file") })
	if !strings.Contains(r.screen(), "other.log") {
		t.Fatalf("header must show new file:\n%s", r.screen())
	}
}

func TestTinyTerminalAndEmptyViewDoNotPanic(t *testing.T) {
	r := newRig(t, "", func(c *config.Config) { c.Follow = true })
	for _, sz := range [][2]int{{1, 1}, {10, 3}, {20, 4}, {200, 60}} {
		r.m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		_ = r.m.View()
		r.press("j", "k", "G", "g", "pgdown", "enter", "esc", "?", "esc", "/", "esc", ":", "esc")
	}
	if !strings.Contains(r.screen(), "no records") {
		t.Fatalf("empty view message:\n%s", r.screen())
	}
}

func TestQuit(t *testing.T) {
	r := newRig(t, logText(3), nil)
	_, cmd := r.m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q must return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected tea.QuitMsg")
	}
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

var _ = regexp.MustCompile
var _ = engine.SevInfo

func sizeMsg(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func sgrParams(seq string) string {
	return regexp.MustCompile(`\x1b\[([0-9;:]*)m`).FindStringSubmatch(seq)[1]
}
