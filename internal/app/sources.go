package app

import (
	"hash/fnv"
	"sort"
	"sync"
)

// maxSourceSlots bounds the registry: a long-running follow of a directory with
// rotating names must not grow it without limit. Beyond it, unknown names get a
// hash-derived slot instead of a registered one.
const maxSourceSlots = 1024

// sourceSlots gives every source file a small stable integer ("slot") so the UI
// can show each file in its own colour. Slots are handed out in registration
// order and never change or get reused while the session runs, so a file keeps
// its colour as others come and go.
type sourceSlots struct {
	mu sync.Mutex
	m  map[string]int
}

func (t *sourceSlots) slot(id string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n, ok := t.m[id]; ok {
		return n
	}
	if len(t.m) >= maxSourceSlots {
		h := fnv.New32a()
		h.Write([]byte(id))
		return int(h.Sum32() & 0x7fffffff)
	}
	if t.m == nil {
		t.m = make(map[string]int)
	}
	n := len(t.m)
	t.m[id] = n
	return n
}

// seed registers names in alphabetical order, so the files present at startup
// get the same colours on every run of the same directory.
func (t *sourceSlots) seed(names []string) {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, n := range sorted {
		t.slot(n)
	}
}

// SourceSlot returns the stable slot of a source (file) name. Files known when
// the session starts get slots in alphabetical order; files that appear later
// take the next free slot. The empty name is slot 0.
func (s *Session) SourceSlot(id string) int {
	if id == "" {
		return 0
	}
	return s.slots.slot(id)
}

// FollowedFileCount is how many sources the view is following right now: the
// files of a live directory, or the services/pods seen in a piped compose or
// kubectl stream (0 otherwise). The UI uses it to decide when there are more
// sources than distinct colours.
func (s *Session) FollowedFileCount() int {
	switch {
	case s.dirf != nil:
		return s.dirf.Count()
	case s.tailer != nil:
		return s.tailer.Sources()
	}
	return 0
}
