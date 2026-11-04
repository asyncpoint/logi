package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/engine"
)

// The detail view of a multi-line NCache record shows the message the way the log
// file has it: one row per line, not everything run together on one row.
func TestDetailViewKeepsMultilineMessageOnSeparateRows(t *testing.T) {
	src, err := os.ReadFile("../../test/fixtures/sample-ncache.log")
	if err != nil {
		t.Skip("fixture not available:", err)
	}
	r := newRig(t, string(src), nil)
	r.indexed()
	r.press("g", "enter")
	if r.m.ov != ovDetail {
		t.Fatal("detail view did not open")
	}
	var rows []string
	for _, l := range strings.Split(r.screen(), "\n") {
		rows = append(rows, strings.TrimRight(engine.StripANSI(l), " "))
	}
	find := func(sub string) int {
		for i, l := range rows {
			if strings.Contains(l, sub) {
				return i
			}
		}
		return -1
	}
	exc := find("OperationFailedException: operation failed")
	at1 := find("at Alachisoft.NCache.Server.Op.Execute()")
	at2 := find("at Alachisoft.NCache.Server.Pipeline.Run()")
	if exc < 0 || at1 < 0 || at2 < 0 {
		t.Fatalf("the continuation lines are missing from the detail view:\n%s", strings.Join(rows, "\n"))
	}
	if !(exc < at1 && at1 < at2) {
		t.Fatalf("rows out of order: exception %d, first frame %d, second frame %d", exc, at1, at2)
	}
	if exc == at1 || at1 == at2 {
		t.Fatalf("lines of the message share a row:\n%s", strings.Join(rows, "\n"))
	}
	// The frames keep the indentation they have in the log (three spaces after the value column).
	if !strings.Contains(rows[at1], "   at Alachisoft") {
		t.Fatalf("stack-frame indentation was lost: %q", rows[at1])
	}
}
