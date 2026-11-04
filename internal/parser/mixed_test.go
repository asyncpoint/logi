package parser

import "testing"

func names(cols []ColumnSpec) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

func eq(a, b []string) bool {
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

func TestMixedColumns(t *testing.T) {
	plain := &Profile{Name: "plain"}
	withEvent := &Profile{Name: "ncache", Columns: []ColumnSpec{
		{Name: "time"}, {Name: "thread", Width: 14}, {Name: "event", Title: "event", Width: 30, Mixed: true}, {Name: "message"},
	}}
	other := &Profile{Name: "other", Columns: []ColumnSpec{{Name: "event", Width: 99, Mixed: true}, {Name: "request_id", Mixed: true}}}

	if got := names(MixedColumns(nil)); !eq(got, names(DefaultColumns())) {
		t.Errorf("no profiles: %v, want the default layout", got)
	}
	if got := names(MixedColumns([]*Profile{plain, nil})); !eq(got, names(DefaultColumns())) {
		t.Errorf("profiles without Mixed columns must not change the layout: %v", got)
	}
	got := MixedColumns([]*Profile{plain, withEvent})
	if want := []string{"time", "level", "source", "event", "message"}; !eq(names(got), want) {
		t.Fatalf("columns = %v, want %v (Mixed column goes just before message; non-Mixed thread is left out)", names(got), want)
	}
	if got[3].Width != 30 || got[2].Hidden != true {
		t.Errorf("event width %d (want 30); source hidden = %v (want true)", got[3].Width, got[2].Hidden)
	}
	if got := names(MixedColumns([]*Profile{withEvent, other})); !eq(got, []string{"time", "level", "source", "event", "request_id", "message"}) {
		t.Errorf("a column shared by two formats appears once, in first-seen order: %v", got)
	}
	// the defaults slice must never be mutated by building a mixed layout
	if names(DefaultColumns())[len(DefaultColumns())-1] != "message" || len(DefaultColumns()) != 4 {
		t.Error("DefaultColumns changed")
	}
}
