package filter

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// FoldMatcher finds a needle in text ignoring case.
//
// ASCII needles use the allocation-free byte search in ContainsFold and match
// ASCII letters in either case. Needles containing non-ASCII text use Unicode
// simple case folding, the same equivalence as strings.EqualFold and regexp's
// (?i): "école" matches "ÉCOLE" and "σας" matches "ΣΑΣ" (all three sigma forms
// are equivalent). Folding is one rune to one rune, so "ß" does not match "SS".
// (An ASCII needle does not match the Kelvin sign or long s, which fold to
// ASCII letters; searching those is not worth slowing every ASCII query.)
type FoldMatcher struct {
	ascii  bool
	lower  string   // lower-cased needle (ASCII path)
	exact  string   // needle that is not valid UTF-8: compared byte for byte
	orbits [][]rune // per needle rune: every rune equivalent under folding
	first  []string // UTF-8 encodings of every case variant of the first rune
}

// NewFoldMatcher compiles needle. It is immutable and safe for concurrent use.
func NewFoldMatcher(needle string) *FoldMatcher {
	m := &FoldMatcher{}
	if isASCII(needle) {
		m.ascii, m.lower = true, strings.ToLower(needle)
		return m
	}
	if !utf8.ValidString(needle) {
		m.exact = needle
		return m
	}
	for _, r := range needle {
		m.orbits = append(m.orbits, orbit(r))
	}
	seen := map[string]bool{}
	for _, v := range m.orbits[0] {
		if s := string(v); !seen[s] {
			seen[s] = true
			m.first = append(m.first, s)
		}
	}
	return m
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// orbit returns r and every rune equal to it under simple case folding.
func orbit(r rune) []rune {
	out := []rune{r}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		out = append(out, f)
	}
	return out
}

// Contains reports whether s contains the needle.
func (m *FoldMatcher) Contains(s string) bool {
	if m.ascii {
		return ContainsFold(s, m.lower)
	}
	start, _ := m.Index(s)
	return start >= 0
}

// Index returns the byte span [start,end) of the first match in s, or (-1,-1).
// The span is in s's own bytes (it can differ in length from the needle, since
// case variants of a rune can have different UTF-8 lengths).
func (m *FoldMatcher) Index(s string) (start, end int) {
	if m.ascii {
		return m.asciiIndex(s)
	}
	if m.exact != "" || len(m.orbits) == 0 {
		i := strings.Index(s, m.exact)
		if i < 0 {
			return -1, -1
		}
		return i, i + len(m.exact)
	}
	// Fixed-size scratch (a rune has at most 4 case variants) keeps the hot
	// path allocation-free; the slice fallback is for exotic needles only.
	var nextBuf [4]int
	var doneBuf [4]bool
	next, done := nextBuf[:], doneBuf[:]
	if len(m.first) > len(nextBuf) {
		next, done = make([]int, len(m.first)), make([]bool, len(m.first))
	}
	for from := 0; from <= len(s); {
		best := -1
		for k, v := range m.first {
			if done[k] {
				continue
			}
			if next[k] < from {
				j := strings.Index(s[from:], v)
				if j < 0 {
					done[k] = true
					continue
				}
				next[k] = from + j
			}
			if best < 0 || next[k] < best {
				best = next[k]
			}
		}
		if best < 0 {
			return -1, -1
		}
		if e := m.matchAt(s, best); e >= 0 {
			return best, e
		}
		from = best + 1
	}
	return -1, -1
}

// matchAt returns the end offset of a match starting exactly at s[p:], or -1.
func (m *FoldMatcher) matchAt(s string, p int) int {
	i := p
	for _, orb := range m.orbits {
		if i >= len(s) {
			return -1
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		ok := false
		for _, o := range orb {
			if o == r {
				ok = true
				break
			}
		}
		if !ok {
			return -1
		}
		i += n
	}
	return i
}

// asciiIndex is Index for ASCII needles (matching is ASCII-case-insensitive).
func (m *FoldMatcher) asciiIndex(s string) (int, int) {
	n := len(m.lower)
	if n == 0 {
		return 0, 0
	}
	if !ContainsFold(s, m.lower) { // fast skip; also the common "no match" answer
		return -1, -1
	}
	for i := 0; i+n <= len(s); i++ {
		if equalFoldASCII(s[i:i+n], m.lower) {
			return i, i + n
		}
	}
	return -1, -1
}
