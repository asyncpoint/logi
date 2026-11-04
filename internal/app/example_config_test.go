package app

import (
	"os"
	"strings"
	"testing"
)

// The example config lists the actions that can be rebound; it must name every
// action the program has, or users cannot discover the newer ones.
func TestExampleConfigListsEveryAction(t *testing.T) {
	src, err := os.ReadFile("../../docs/config.example.yaml")
	if err != nil {
		t.Skip("example config not available:", err)
	}
	var doc strings.Builder
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, "#") {
			doc.WriteString(l[1:] + " ")
		}
	}
	words := map[string]bool{}
	for _, w := range strings.Fields(strings.NewReplacer(".", " ", ",", " ", ":", " ").Replace(doc.String())) {
		words[w] = true
	}
	for _, c := range DefaultCommands().All() {
		if !words[string(c.Action)] {
			t.Errorf("docs/config.example.yaml does not list action %q", c.Action)
		}
	}
}
