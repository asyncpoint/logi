package tui

import (
	"strings"
	"testing"
)

// hits counts the search highlights drawn in the list: text in the hit style (bold
// black on yellow). The last line is the status bar, whose W: badge uses the same
// colours, so it is left out.
func hits(r *rig) int {
	lines := strings.Split(r.raw(), "\n")
	return strings.Count(strings.Join(lines[:len(lines)-1], "\n"), "30;43m")
}

func searchFor(t *testing.T, r *rig, text string) {
	t.Helper()
	r.press("/")
	r.typ(text)
	r.settle("match for "+text, func() bool { return strings.Contains(r.screen(), "match at #") })
}

// Emptying the search box removes every highlight, including the one for the
// last remaining character (the reported bug), however the box is emptied.
func TestEmptyingTheSearchBoxClearsTheHighlighting(t *testing.T) {
	cases := map[string]func(r *rig, query string){
		"backspace": func(r *rig, q string) {
			for range q {
				r.press("backspace")
			}
		},
		"ctrl+u": func(r *rig, q string) { r.press("ctrl+u") },
		"ctrl+w": func(r *rig, q string) { r.press("ctrl+w", "ctrl+w") },
	}
	for name, empty := range cases {
		for _, q := range []string{"e", "message 1"} {
			t.Run(name+"/"+q, func(t *testing.T) {
				r := newRig(t, logText(15), nil) // fits on screen, so the text is always visible
				r.indexed()
				r.m.Update(sizeMsg(160, 40))
				searchFor(t, r, q)
				if r.m.sess.Matcher() == nil || hits(r) == 0 {
					t.Fatalf("precondition: the search for %q should highlight the visible text", q)
				}
				empty(r, q)
				if r.m.input != "" {
					t.Fatalf("the box should be empty, has %q", r.m.input)
				}
				if r.m.sess.Matcher() != nil {
					t.Fatal("the session still holds the old matcher, so the last character stays highlighted")
				}
				if n := hits(r); n != 0 {
					t.Fatalf("%d highlighted spans survived an empty search box:\n%s", n, r.screen())
				}
				if r.m.sfoundID != 0 || r.m.sPending || r.m.sstate != "" {
					t.Fatalf("search state not reset: found=%d pending=%v state=%q", r.m.sfoundID, r.m.sPending, r.m.sstate)
				}
			})
		}
	}
}

// Erasing the text returns the cursor to where the search began, as Esc does, and
// a new search afterwards works normally.
func TestEmptyingTheSearchBoxReturnsToTheStartingPoint(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	r.m.Update(sizeMsg(160, 24))
	r.press("down", "down") // cursor on the third record
	start := r.m.cursorID
	searchFor(t, r, "message 25")
	if r.m.cursorID == start {
		t.Fatal("precondition: the search should have moved the cursor")
	}
	r.press("ctrl+u")
	if r.m.cursorID != start {
		t.Fatalf("cursor at %d after clearing the box, want the starting record %d", r.m.cursorID, start)
	}
	r.typ("message 7")
	r.settle("second search", func() bool { return strings.Contains(r.screen(), "match at #") })
	if r.m.sess.Matcher() == nil {
		t.Fatal("typing again must search again")
	}
}

// Ctrl+W shortens the query and the search follows (it used to keep the old one).
func TestCtrlWReSearchesWithTheShorterQuery(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	r.m.Update(sizeMsg(160, 24))
	searchFor(t, r, "message 2")
	r.press("ctrl+w")
	if got := r.m.searchReq.Query; got != "message " {
		t.Fatalf("query after Ctrl+W = %q, want %q", got, "message ")
	}
	if m := r.m.sess.Matcher(); m == nil || m.Request().Query != "message " {
		t.Fatalf("the session should be searching for the shorter query, has %+v", m)
	}
}

// Closing the box and using n/N with nothing to search for does nothing, quietly.
func TestSearchKeysWithAnEmptyQueryAreHarmless(t *testing.T) {
	r := newRig(t, logText(30), nil)
	r.indexed()
	searchFor(t, r, "message 5")
	r.press("ctrl+u", "esc", "n", "N")
	if r.m.sess.Matcher() != nil {
		t.Fatal("no matcher expected after clearing the query")
	}
}
