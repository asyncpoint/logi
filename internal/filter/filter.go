// Package filter compiles a small filter language into predicates that are
// evaluated once per record as it arrives.
//
// Terms are space separated and ANDed; a leading '-' negates a term:
//
//	severity:error            exact level        severity:>=warn   level range
//	severity:warn,error       any of             source:ncache     source id contains
//	message:"conn failed"     message contains   key=value         structured field equals
//	re:timeout\s+\d+          explicit regex     "exact phrase"    raw text contains
//	word                      raw text contains  -word / -severity:debug   negation
package filter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/parser"
)

// Predicate decides whether a record is visible.
type Predicate interface {
	Match(r *engine.LogRecord) bool
}

type predFunc func(r *engine.LogRecord) bool

func (f predFunc) Match(r *engine.LogRecord) bool { return f(r) }

type all []Predicate

func (a all) Match(r *engine.LogRecord) bool {
	for _, p := range a {
		if !p.Match(r) {
			return false
		}
	}
	return true
}

// rawPred is a predicate that only inspects the record's raw text, so stores
// can evaluate raw before parsing.
type rawPred struct {
	predFunc
	raw func(string) bool
}

// RawPrefilter returns a necessary condition on a record's raw text derived from
// the text-only terms of p (nil if there are none). Terms that need parsed
// fields are ignored here and still evaluated by p.Match afterwards.
func RawPrefilter(p Predicate) func(string) bool {
	var fs []func(string) bool
	switch v := p.(type) {
	case rawPred:
		fs = append(fs, v.raw)
	case all:
		for _, t := range v {
			if rp, ok := t.(rawPred); ok {
				fs = append(fs, rp.raw)
			}
		}
	}
	switch len(fs) {
	case 0:
		return nil
	case 1:
		return fs[0]
	}
	return func(s string) bool {
		for _, f := range fs {
			if !f(s) {
				return false
			}
		}
		return true
	}
}

// Compile parses q. An empty query yields (nil, nil): no filtering.
func Compile(q string) (Predicate, error) { return CompileWith(q, Options{}) }

// CompileWith is Compile with options (see Options).
func CompileWith(q string, o Options) (Predicate, error) {
	toks, err := tokenize(q)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, nil
	}
	var ps all
	for _, t := range toks {
		p, err := compileTerm(t, o)
		if err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	if len(ps) == 1 {
		return ps[0], nil
	}
	return ps, nil
}

func tokenize(q string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	inQuote, has := false, false
	for _, c := range q {
		switch {
		case c == '"':
			inQuote, has = !inQuote, true
		case c == ' ' && !inQuote, c == '\t' && !inQuote:
			if has {
				toks = append(toks, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(c)
			has = true
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quote")
	}
	if has {
		toks = append(toks, cur.String())
	}
	return toks, nil
}

func compileTerm(t string, o Options) (Predicate, error) {
	neg := false
	if len(t) > 1 && t[0] == '-' {
		neg, t = true, t[1:]
	}
	p, err := compileBare(t, o)
	if err != nil {
		return nil, err
	}
	if neg {
		return predFunc(func(r *engine.LogRecord) bool { return !p.Match(r) }), nil
	}
	return p, nil
}

func text(r *engine.LogRecord) string {
	if r.Raw.Data != "" {
		return r.Raw.Data
	}
	return r.Message
}

func compileBare(t string, o Options) (Predicate, error) {
	if i := strings.IndexByte(t, ':'); i > 0 {
		key, val := strings.ToLower(t[:i]), t[i+1:]
		switch key {
		case "severity", "level", "sev":
			return severityPred(val)
		case "source", "src":
			fm := NewFoldMatcher(val)
			return predFunc(func(r *engine.LogRecord) bool { return fm.Contains(r.SourceID) }), nil
		case "message", "msg":
			fm := NewFoldMatcher(val)
			return predFunc(func(r *engine.LogRecord) bool { return fm.Contains(r.Message) }), nil
		case "since":
			return compileTime(val, false, o)
		case "until":
			return compileTime(val, true, o)
		case "re", "regex":
			re, err := regexp.Compile(val)
			if err != nil {
				return nil, fmt.Errorf("bad regex: %v", err)
			}
			return rawPred{func(r *engine.LogRecord) bool { return re.MatchString(text(r)) }, re.MatchString}, nil
		}
	}
	if i := strings.IndexByte(t, '='); i > 0 && !strings.ContainsAny(t[:i], " ") {
		key, want := t[:i], strings.ToLower(t[i+1:])
		return predFunc(func(r *engine.LogRecord) bool {
			v, ok := FieldValue(r, key)
			return ok && EqualFold(v, want)
		}), nil
	}
	fm := NewFoldMatcher(t)
	return rawPred{func(r *engine.LogRecord) bool { return fm.Contains(text(r)) }, fm.Contains}, nil
}

func severityPred(v string) (Predicate, error) {
	op := "="
	for _, o := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(v, o) {
			op, v = o, v[len(o):]
			break
		}
	}
	var set [engine.NumSeverities]bool
	for _, part := range strings.Split(v, ",") {
		s := engine.ParseSeverity(strings.TrimSpace(part))
		if s == engine.SevUnknown {
			return nil, fmt.Errorf("unknown severity %q", part)
		}
		for i := 0; i < engine.NumSeverities; i++ {
			sv := engine.Severity(i)
			ok := false
			switch op {
			case "=":
				ok = sv == s
			case ">=":
				ok = sv >= s
			case ">":
				ok = sv > s
			case "<=":
				ok = sv <= s && sv != engine.SevUnknown
			case "<":
				ok = sv < s && sv != engine.SevUnknown
			}
			if ok {
				set[i] = true
			}
		}
	}
	return predFunc(func(r *engine.LogRecord) bool { return set[r.Severity] }), nil
}

// FieldValue returns a structured field of r as text: from Fields if the
// parser produced them, otherwise by scanning JSON raw text on demand.
func FieldValue(r *engine.LogRecord, key string) (string, bool) {
	if r.Fields != nil {
		if v, ok := r.Fields.Get(key); ok {
			return v.Value, true
		}
	}
	if t := text(r); len(t) > 0 && t[0] == '{' {
		return parser.Field(t, key)
	}
	return "", false
}

// EqualFold compares s with an already-lowercase value without allocating for
// ASCII text. Unicode values fall back to strings.EqualFold for correctness.
func EqualFold(s, lower string) bool {
	for i := 0; i < len(s) && i < len(lower); i++ {
		if s[i] >= 0x80 || lower[i] >= 0x80 {
			return strings.EqualFold(s, lower)
		}
	}
	if len(s) != len(lower) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		d := lower[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != d {
			return false
		}
	}
	return true
}

// ContainsFold reports whether s contains lowerSub, ignoring case. For an ASCII
// lowerSub (already lower case) it is the allocation-free fast path; a needle
// with non-ASCII text is matched with Unicode folding (see FoldMatcher), which
// allocates, so hot paths should compile a FoldMatcher once instead.
func ContainsFold(s, lowerSub string) bool {
	if !isASCII(lowerSub) {
		return NewFoldMatcher(lowerSub).Contains(s)
	}
	n := len(lowerSub)
	if n == 0 {
		return true
	}
	if n > len(s) {
		return false
	}
	// Anchor on the least common byte of the needle and let the assembly-backed
	// IndexByte skip to its (lower/upper case) occurrences instead of testing
	// every byte of the haystack.
	k := rarestByte(lowerSub)
	lo := lowerSub[k]
	up := lo
	if lo >= 'a' && lo <= 'z' {
		up = lo - 32
	}
	hay := s[k : len(s)-n+k+1] // anchor positions whose match start is in range
	lDone, uDone := false, up == lo
	pl, pu := 0, 0
	for i := 0; ; {
		if !lDone && pl < i {
			if j := strings.IndexByte(hay[i:], lo); j < 0 {
				lDone = true
			} else {
				pl = i + j
			}
		}
		if !uDone && pu < i {
			if j := strings.IndexByte(hay[i:], up); j < 0 {
				uDone = true
			} else {
				pu = i + j
			}
		}
		var p int
		switch {
		case lDone && uDone:
			return false
		case lDone:
			p = pu
		case uDone:
			p = pl
		default:
			p = min(pl, pu)
		}
		if equalFoldASCII(s[p:p+n], lowerSub) {
			return true
		}
		i = p + 1
	}
}

func equalFoldASCII(s, lower string) bool {
	for j := 0; j < len(lower); j++ {
		c := s[j]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c != lower[j] {
			return false
		}
	}
	return true
}

// commonness ranks English-ish letter frequency; unknown bytes are rarest.
const commonOrder = " etaoinsrhldcumfpgwybvkxjqz"

func rarestByte(s string) int {
	best, bestScore := 0, -1
	for i := 0; i < len(s); i++ {
		score := len(commonOrder) + 1 // not a common letter: rare
		if j := strings.IndexByte(commonOrder, s[i]); j >= 0 {
			score = j
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}
