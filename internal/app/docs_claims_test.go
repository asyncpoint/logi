package app

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// docs/HOWTO.md shows "--since 2h --until 30m": a window measured back from the
// newest record on both ends.
func TestRelativeSinceAndUntilCountBackFromTheNewestRecord(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ { // 10:00 .. 14:50, ten minutes apart
		m := 10*60 + i*10
		fmt.Fprintf(&b, "2026-09-19 %02d:%02d:00 INFO tick %d\n", m/60, m%60, i)
	}
	s := newSession(t, b.String(), nil)
	waitFor(t, "index", func() bool { return s.Status().IndexDone })
	s.SetFilter("since:2h until:30m") // 12:50 .. 14:20 inclusive
	waitFor(t, "filter", func() bool { st := s.Status(); return !st.Filtering && st.ViewRows > 0 })
	if got := s.Status().ViewRows; got != 10 {
		t.Fatalf("since:2h until:30m kept %d records, want 10 (12:50 through 14:20)", got)
	}
}

// docedKey renders a default key binding the way the README key table writes it.
func docedKey(k string) string {
	names := map[string]string{"pgdown": "PgDn", "pgup": "PgUp", "down": "↓", "up": "↑", "left": "←", "right": "→",
		"home": "Home", "end": "End", "enter": "Enter", "tab": "Tab", "space": "Space", "esc": "Esc"}
	parts := strings.Split(k, "+")
	last := parts[len(parts)-1]
	var out []string
	for _, p := range parts[:len(parts)-1] {
		out = append(out, strings.ToUpper(p[:1])+p[1:])
	}
	shift := false
	if len(last) == 1 && last[0] >= 'A' && last[0] <= 'Z' && len(parts) > 1 {
		shift = true // alt+E is Alt+Shift+E
	}
	switch {
	case names[last] != "":
		last = names[last]
	case len(last) == 2 && last[0] == 'f', len(last) == 3 && last[0] == 'f': // f1, f3
		last = strings.ToUpper(last)
	case len(parts) > 1:
		last = strings.ToUpper(last)
	}
	if shift {
		out = append(out, "Shift")
	}
	return strings.Join(append(out, last), "+")
}

// Every default key binding is listed in the README key table.
func TestReadmeListsEveryDefaultKey(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Skip("README.md not available:", err)
	}
	readme := string(b)
	for _, c := range DefaultCommands().All() {
		for _, k := range c.Keys {
			doc := docedKey(k)
			// shift+<key> spellings of an already documented Shift combination are the same key
			if strings.HasPrefix(k, "alt+shift+") && strings.Contains(readme, "`"+docedKey("alt+"+strings.ToUpper(strings.TrimPrefix(k, "alt+shift+")))+"`") {
				continue
			}
			if !strings.Contains(readme, "`"+doc+"`") {
				t.Errorf("README.md key table does not list %q (%s) for %s", doc, k, c.Title)
			}
		}
	}
}
