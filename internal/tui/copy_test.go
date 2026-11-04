package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/config"
)

// runCopy executes a copy command and the clipboard command Update returns for
// it, and reports the text handed to the terminal ("" if nothing was copied).
func runCopy(t *testing.T, r *rig, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	cm, ok := cmd().(copyMsg)
	if !ok {
		t.Fatal("copy command did not produce a copyMsg")
	}
	_, clip := r.m.Update(cm)
	if clip == nil {
		return ""
	}
	return fmt.Sprint(clip()) // the clipboard message is a string type
}

func TestCopyCurrentRecord(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	r.press("g", "down", "down") // third record
	_, cmd := r.m.Update(key("y"))
	got := runCopy(t, r, cmd)
	if want := "2026-09-19 10:00:03 INFO message 3"; got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
	if !strings.Contains(r.m.msg, "copied 1 record") {
		t.Fatalf("status = %q", r.m.msg)
	}
}

func TestCopySelectedRange(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	r.press("g", "down", "v", "down", "down", "v") // records 2..4
	_, cmd := r.m.Update(key("y"))
	got := runCopy(t, r, cmd)
	want := strings.Join([]string{
		"2026-09-19 10:00:02 INFO message 2",
		"2026-09-19 10:00:03 INFO message 3",
		"2026-09-19 10:00:04 INFO message 4",
	}, "\n")
	if got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
	if r.m.selectionStart != 0 || r.m.selectionEnd != 0 {
		t.Fatal("selection should be cleared after a successful copy")
	}
	if !strings.Contains(r.m.msg, "copied 3 records") {
		t.Fatalf("status = %q", r.m.msg)
	}
}

func TestCopyReverseSelectionKeepsViewOrder(t *testing.T) {
	r := newRig(t, logText(10), nil)
	r.indexed()
	a, _ := r.m.sess.IDAt(1)
	b, _ := r.m.sess.IDAt(3)
	r.m.selectionStart, r.m.selectionEnd = b, a
	_, cmd := r.m.Update(key("y"))
	got := runCopy(t, r, cmd)
	if lines := strings.Split(got, "\n"); len(lines) != 3 || !strings.HasSuffix(lines[0], "message 2") || !strings.HasSuffix(lines[2], "message 4") {
		t.Fatalf("copied %q", got)
	}
}

func TestCopyFollowsFilteredView(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	if err := r.s.SetFilter("severity:error"); err != nil { // records 10, 20, 30
		t.Fatal(err)
	}
	r.settle("filtered view", func() bool { st := r.s.Status(); return !st.Filtering && st.ViewRows == 3 })
	first, _ := r.m.sess.IDAt(0)
	last, _ := r.m.sess.IDAt(2)
	r.m.selectionStart, r.m.selectionEnd = first, last
	_, cmd := r.m.Update(key("y"))
	got := runCopy(t, r, cmd)
	if lines := strings.Split(got, "\n"); len(lines) != 3 || !strings.Contains(got, "ERROR message 10") || !strings.Contains(got, "ERROR message 30") || strings.Contains(got, "INFO") {
		t.Fatalf("copied %q", got)
	}
}

func TestCopyFromDetailView(t *testing.T) {
	r := newRig(t, logText(10), nil)
	r.indexed()
	r.press("g", "down", "enter") // open record 2
	if r.m.ov != ovDetail {
		t.Fatal("detail view not open")
	}
	_, cmd := r.m.Update(key("y"))
	if got, want := runCopy(t, r, cmd), "2026-09-19 10:00:02 INFO message 2"; got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
	if r.m.ov != ovDetail {
		t.Fatal("copying should not close the detail view")
	}
}

func TestCopyStopsAtClipboardLimit(t *testing.T) {
	var b strings.Builder
	pad := strings.Repeat("x", 300)
	for i := 1; i <= 3000; i++ { // ~900 KiB
		fmt.Fprintf(&b, "2026-09-19 10:00:00 INFO %04d %s\n", i, pad)
	}
	r := newRig(t, b.String(), nil)
	r.indexed()
	first, _ := r.m.sess.IDAt(0)
	last, _ := r.m.sess.IDAt(2999)
	r.m.selectionStart, r.m.selectionEnd = first, last
	_, cmd := r.m.Update(key("y"))
	got := runCopy(t, r, cmd)
	if len(got) == 0 || len(got) > maxCopyBytes {
		t.Fatalf("copied %d bytes, limit %d", len(got), maxCopyBytes)
	}
	if strings.HasSuffix(got, "x") == false || strings.Count(got, "\n")+1 >= 3000 {
		t.Fatalf("expected a cut at a record boundary, got %d lines", strings.Count(got, "\n")+1)
	}
	if !strings.Contains(r.m.msg, "clipboard limit") {
		t.Fatalf("status should explain the cut: %q", r.m.msg)
	}
}

func TestCopyIsBoundAndListed(t *testing.T) {
	r := newRig(t, logText(3), nil)
	if act, ok := r.m.cmds.Lookup("y"); !ok || act != app.ActionCopy {
		t.Fatalf("y resolves to %q, %v", act, ok)
	}
}

// A static file is read from disk (the block cache holds no raw text) and may
// contain colour codes; the clipboard must get plain text either way.
func TestCopyFromStaticFileStripsColourCodes(t *testing.T) {
	content := "2026-09-19 10:00:01 INFO plain one\n" +
		"\x1b[32m2026-09-19 10:00:02\x1b[0m \x1b[31mERROR\x1b[0m coloured two\n" +
		"2026-09-19 10:00:03 INFO plain three\n"
	r := newRig(t, content, func(c *config.Config) { c.Follow, c.Backlog = false, 0 })
	r.indexed()
	if r.s.Mode() != app.ModeStatic {
		t.Fatalf("mode = %v, want static", r.s.Mode())
	}
	first, _ := r.m.sess.IDAt(0)
	last, _ := r.m.sess.IDAt(2)
	r.m.selectionStart, r.m.selectionEnd = first, last
	_, cmd := r.m.Update(key("y"))
	got := runCopy(t, r, cmd)
	want := "2026-09-19 10:00:01 INFO plain one\n2026-09-19 10:00:02 ERROR coloured two\n2026-09-19 10:00:03 INFO plain three"
	if got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
}
