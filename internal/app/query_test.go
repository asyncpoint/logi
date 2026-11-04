package app

import (
	"testing"

	"github.com/asyncpoint/logi/internal/filter"
)

func TestFilteredViewRejectsStaleBatchAndCapsMatches(t *testing.T) {
	v := filteredView{wake: make(chan struct{}, 1)}
	pred, err := filter.Compile("severity:error")
	if err != nil {
		t.Fatal(err)
	}
	oldGen := v.begin("severity:error", pred, false)
	newGen := v.begin("severity:error", pred, false)
	batch := []uint64{1, 2}
	if accepted, _ := v.commit(oldGen, batch, 2, 1, 1); accepted {
		t.Fatal("stale filter batch was accepted")
	}
	if accepted, capped := v.commit(newGen, batch, 2, 1, 1); !accepted || !capped {
		t.Fatalf("commit accepted=%v capped=%v, want true/true", accepted, capped)
	}
	if got := v.rowCount(99); got != 1 {
		t.Fatalf("row count=%d, want 1", got)
	}
	if next, _, _ := v.cursor(); next != nil {
		t.Fatal("capped view should stop scanning until the filter changes")
	}
	if id, ok, active := v.idAt(0); !active || !ok || id != 1 {
		t.Fatalf("idAt=(%d,%v,%v), want (1,true,true)", id, ok, active)
	}
	if _, ok, active := v.idAt(1); !active || ok {
		t.Fatalf("out-of-range idAt active=%v ok=%v", active, ok)
	}
}

func TestFilteredViewTrimsEvictedIDs(t *testing.T) {
	v := filteredView{}
	pred, err := filter.Compile("severity:error")
	if err != nil {
		t.Fatal(err)
	}
	gen := v.begin("severity:error", pred, false)
	ids := []uint64{1, 2, 3}
	if ok, _ := v.commit(gen, ids, 3, 1, 10); !ok {
		t.Fatal("commit rejected")
	}
	v.trim(3)
	if got := v.rowCount(100); got != 1 {
		t.Fatalf("rows after eviction=%d, want 1", got)
	}
	if id, ok, active := v.idAt(0); !active || !ok || id != 3 {
		t.Fatalf("remaining row=(%d,%v,%v), want ID 3", id, ok, active)
	}
}

func TestFilteredViewLiveCapKeepsNewestAndContinues(t *testing.T) {
	v := filteredView{}
	pred, err := filter.Compile("severity:error")
	if err != nil {
		t.Fatal(err)
	}
	gen := v.begin("severity:error", pred, true)
	if ok, capped := v.commit(gen, []uint64{1, 2, 3}, 3, 1, 2); !ok || !capped {
		t.Fatalf("first commit accepted=%v capped=%v", ok, capped)
	}
	if got, ok, active := v.idAt(0); !active || !ok || got != 2 {
		t.Fatalf("oldest retained ID=(%d,%v,%v), want 2", got, ok, active)
	}
	if pred, _, from := v.cursor(); pred == nil || from != 4 {
		t.Fatalf("live capped view stopped catching up: pred=%v from=%d", pred, from)
	}
	if ok, capped := v.commit(gen, []uint64{4}, 4, 2, 2); !ok || !capped {
		t.Fatalf("second commit accepted=%v capped=%v", ok, capped)
	}
	if got, ok, _ := v.idAt(0); !ok || got != 3 {
		t.Fatalf("newest window starts at %d, want 3", got)
	}
}

func TestFilteredViewClearsAfterLiveRingEmpties(t *testing.T) {
	v := filteredView{}
	pred, err := filter.Compile("severity:error")
	if err != nil {
		t.Fatal(err)
	}
	gen := v.begin("severity:error", pred, true)
	if ok, _ := v.commit(gen, []uint64{1, 2, 3}, 3, 1, 10); !ok {
		t.Fatal("commit rejected")
	}
	v.trim(0)
	if got := v.rowCount(99); got != 0 {
		t.Fatalf("rows after ring clear=%d, want 0", got)
	}
}

func BenchmarkFilteredViewLiveCappedCommit(b *testing.B) {
	pred, err := filter.Compile("severity:error")
	if err != nil {
		b.Fatal(err)
	}
	const capMatches = 50000
	v := filteredView{}
	gen := v.begin("severity:error", pred, true)
	initial := make([]uint64, capMatches)
	for i := range initial {
		initial[i] = uint64(i + 1)
	}
	if ok, _ := v.commit(gen, initial, capMatches, 1, capMatches); !ok {
		b.Fatal("initial commit rejected")
	}
	batch := []uint64{0, 0, 0, 0}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		base := uint64(capMatches + i*len(batch) + 1)
		for j := range batch {
			batch[j] = base + uint64(j)
		}
		if ok, _ := v.commit(gen, batch, base+uint64(len(batch))-1, 1, capMatches); !ok {
			b.Fatal("commit rejected")
		}
	}
}
