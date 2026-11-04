// Package search compiles user searches into matchers.
// Literal matching is the default; regex is opt-in and uses Go's RE2 engine,
// which guarantees linear-time matching on hostile input.
package search

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/filter"
)

type Mode int

const (
	Literal Mode = iota
	Regex
	Field // "key=value": structured field equals value
)

func (m Mode) String() string {
	if names := [...]string{"literal", "regex", "field"}; m >= 0 && int(m) < len(names) {
		return names[m]
	}
	return "unknown"
}

// ParseMode maps a config string to a Mode.
func ParseMode(s string) Mode {
	switch strings.ToLower(s) {
	case "regex":
		return Regex
	case "field":
		return Field
	}
	return Literal
}

// Request describes one search.
type Request struct {
	Query         string
	Mode          Mode
	CaseSensitive bool
}

// Matcher tests records and locates matches for highlighting.
type Matcher struct {
	req   Request
	lower string
	fold  *filter.FoldMatcher // case-insensitive literal mode
	re    *regexp.Regexp
	key   string
	want  string
}

func (r Request) String() string { return fmt.Sprintf("%s(%q)", r.Mode, r.Query) }

// Compile prepares a request. An empty query is an error-free no-op matcher
// that matches nothing.
func Compile(req Request) (*Matcher, error) {
	m := &Matcher{req: req}
	switch req.Mode {
	case Regex:
		q := req.Query
		if !req.CaseSensitive {
			q = "(?i)" + q
		}
		re, err := regexp.Compile(q)
		if err != nil {
			return nil, err
		}
		m.re = re
	case Field:
		i := strings.IndexByte(req.Query, '=')
		if i <= 0 {
			return nil, fmt.Errorf("field search needs key=value")
		}
		m.key, m.want = req.Query[:i], req.Query[i+1:]
		if !req.CaseSensitive {
			m.want = strings.ToLower(m.want)
		}
	default:
		m.lower = req.Query
		if !req.CaseSensitive {
			m.lower = strings.ToLower(req.Query)
			m.fold = filter.NewFoldMatcher(req.Query)
		}
	}
	return m, nil
}

func (m *Matcher) Request() Request { return m.req }

func textOf(r *engine.LogRecord) string {
	if r.Raw.Data != "" {
		return r.Raw.Data
	}
	return r.Message
}

// Match reports whether the record matches.
func (m *Matcher) Match(r *engine.LogRecord) bool {
	if m.req.Query == "" {
		return false
	}
	switch m.req.Mode {
	case Regex:
		return m.re.MatchString(textOf(r))
	case Field:
		v, ok := filter.FieldValue(r, m.key)
		if !ok {
			return false
		}
		if !m.req.CaseSensitive {
			return filter.EqualFold(v, m.want)
		}
		return v == m.want
	}
	if m.req.CaseSensitive {
		return strings.Contains(textOf(r), m.lower)
	}
	return m.fold.Contains(textOf(r))
}

// Spans returns [start,end) byte ranges of matches within s, for highlighting
// the few visible rows. Field mode highlights nothing.
func (m *Matcher) Spans(s string) [][2]int {
	if m.req.Query == "" {
		return nil
	}
	switch m.req.Mode {
	case Regex:
		var out [][2]int
		for _, ix := range m.re.FindAllStringIndex(s, 16) {
			if ix[1] > ix[0] {
				out = append(out, [2]int{ix[0], ix[1]})
			}
		}
		return out
	case Field:
		return nil
	}
	var out [][2]int
	if !m.req.CaseSensitive {
		for from := 0; len(out) < 16 && from < len(s); {
			a, b := m.fold.Index(s[from:])
			if a < 0 {
				break
			}
			if b > a {
				out = append(out, [2]int{from + a, from + b})
			}
			from += max(b, a+1)
		}
		return out
	}
	for from := 0; len(out) < 16; {
		i := strings.Index(s[from:], m.lower)
		if i < 0 {
			break
		}
		out = append(out, [2]int{from + i, from + i + len(m.lower)})
		from += i + len(m.lower)
	}
	return out
}

// NeedsRaw reports whether Match inspects a record's full raw text. Stores that
// keep records without their raw text (the static block cache) must supply it
// first, or the match would run against the stripped Message only.
func (m *Matcher) NeedsRaw() bool { return m.req.Query != "" && m.req.Mode != Field }

// RawKeep returns a necessary-condition test on a record's raw text, letting a
// store skip parsing records that cannot match, or nil when the mode needs
// parsed fields. It agrees with Match: Match(r) implies RawKeep()(r.Raw.Data).
func (m *Matcher) RawKeep() func(string) bool {
	if m.req.Query == "" {
		return nil
	}
	switch m.req.Mode {
	case Regex:
		return m.re.MatchString
	case Field:
		return nil
	}
	if m.req.CaseSensitive {
		return func(s string) bool { return strings.Contains(s, m.lower) }
	}
	return m.fold.Contains
}
