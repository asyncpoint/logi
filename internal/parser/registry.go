package parser

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
)

// ColumnSpec declares one column of the record list. Name is either a built-in
// ("time", "level", "source", "message") or the key of a record field produced
// by the parser (e.g. "thread"). Columns appear in the order declared, which for
// tabular formats mirrors the file's own header.
type ColumnSpec struct {
	Name   string
	Title  string
	Width  int  // fixed display width for field columns (0 = default)
	Hidden bool // hidden until the user shows it
	// Mixed marks a column worth showing when files of different formats are
	// merged into one view (see MixedColumns), e.g. NCache's event name.
	Mixed bool
}

// DefaultColumns is the layout of formats that declare none.
func DefaultColumns() []ColumnSpec {
	return []ColumnSpec{
		{Name: "time", Title: "time"},
		{Name: "level", Title: "level"},
		{Name: "source", Title: "source", Hidden: true}, // shown automatically when several files are merged
		{Name: "message", Title: "message"},
	}
}

// MixedColumns is the layout of a view that merges files of different formats:
// the generic columns plus every column a member format marks Mixed, placed just
// before the message. Records of formats without such a field simply show an
// empty cell, and with no Mixed columns this is exactly DefaultColumns.
func MixedColumns(profiles []*Profile) []ColumnSpec {
	base := DefaultColumns()
	seen := make(map[string]bool, len(base))
	for _, c := range base {
		seen[c.Name] = true
	}
	var extra []ColumnSpec
	for _, p := range profiles {
		if p == nil {
			continue
		}
		for _, c := range p.Columns {
			if c.Mixed && !seen[c.Name] {
				seen[c.Name] = true
				extra = append(extra, c)
			}
		}
	}
	if len(extra) == 0 {
		return base
	}
	out := make([]ColumnSpec, 0, len(base)+len(extra))
	for _, c := range base {
		if c.Name == "message" {
			out = append(out, extra...)
		}
		out = append(out, c)
	}
	return out
}

// Profile bundles what logi needs to understand one log format.
type Profile struct {
	Name        string
	Description string
	// Detect scores a small sample: 0 = definitely not, 1 = certain.
	Detect func(sample []byte) float64
	// NewParser returns a parser instance (parsers are stateless but may pool).
	NewParser func() Parser
	// NewParserFor optionally builds a parser tuned to the sample (for example
	// from a column header at the top of the file). nil => NewParser.
	NewParserFor func(sample []byte) Parser
	// Columns optionally declares the list columns. nil => DefaultColumns.
	Columns []ColumnSpec
	// NewFramer optionally overrides generic framing. nil => framing.Detect.
	NewFramer func(sample []byte, lim engine.Limits, offset int64) framing.Framer
}

// Registry holds the known profiles. Registration order breaks score ties in
// favour of later (more specialised) profiles.
type Registry struct {
	byName map[string]*Profile
	order  []string
}

func NewRegistry() *Registry { return &Registry{byName: map[string]*Profile{}} }

func (r *Registry) Register(p Profile) error {
	if p.Name == "" || p.NewParser == nil {
		return fmt.Errorf("parser: invalid profile %q", p.Name)
	}
	if _, dup := r.byName[p.Name]; dup {
		return fmt.Errorf("parser: profile %q already registered", p.Name)
	}
	cp := p
	r.byName[p.Name] = &cp
	r.order = append(r.order, p.Name)
	return nil
}

func (r *Registry) Get(name string) (*Profile, bool) { p, ok := r.byName[name]; return p, ok }

func (r *Registry) Names() []string {
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

// minConfidence is the lowest detection score that beats the generic fallback.
const minConfidence = 0.5

// Detect picks the best profile for sample; generic is the fallback.
func (r *Registry) Detect(sample []byte) *Profile {
	var best *Profile
	bestScore := 0.0
	for _, name := range r.order {
		p := r.byName[name]
		if p.Detect == nil || name == "generic" {
			continue
		}
		if s := p.Detect(sample); s >= minConfidence && s >= bestScore {
			best, bestScore = p, s
		}
	}
	if best == nil {
		best = r.byName["generic"]
	}
	return best
}

// Plugin is a built-in architectural extension point. It is not a
// dynamically loaded module.
type Plugin interface {
	Name() string
	Description() string
	EnabledByDefault() bool
	Register(r *Registry) error
}

// WithYearRef returns p configured to resolve year-less timestamps relative to
// ref (typically the log file's modification time). Parsers that do not parse
// year-less timestamps are returned unchanged.
func WithYearRef(p Parser, ref time.Time) Parser {
	if g, ok := p.(Generic); ok && !ref.IsZero() {
		g.Ref = ref
		return g
	}
	return p
}

// ParserFor returns a parser for the sample, honouring NewParserFor.
func (p *Profile) ParserFor(sample []byte) Parser {
	if p.NewParserFor != nil {
		return p.NewParserFor(sample)
	}
	return p.NewParser()
}

// ColumnLayout returns the declared columns or the defaults.
func (p *Profile) ColumnLayout() []ColumnSpec {
	if p == nil || len(p.Columns) == 0 {
		return DefaultColumns()
	}
	return p.Columns
}

// Framer builds the framer for a profile.
func (p *Profile) Framer(sample []byte, lim engine.Limits, offset int64) framing.Framer {
	if p.NewFramer != nil {
		return p.NewFramer(sample, lim, offset)
	}
	return framing.Detect(sample, lim, offset)
}

// --- built-in profiles -------------------------------------------------

// GenericProfile is the always-available fallback.
func GenericProfile() Profile {
	return Profile{
		Name:        "generic",
		Description: "Timestamp/severity heuristics for plain-text logs",
		Detect:      func([]byte) float64 { return 0.1 },
		NewParser:   func() Parser { return Generic{} },
	}
}

// JSONProfile handles one-JSON-object-per-line logs.
func JSONProfile() Profile {
	return Profile{
		Name:        "json",
		Description: "JSON Lines (one object per line)",
		Detect:      detectJSON,
		NewParser:   func() Parser { return JSON{} },
		NewFramer: func(_ []byte, lim engine.Limits, offset int64) framing.Framer {
			return framing.NewLineFramer(lim, offset)
		},
	}
}

func detectJSON(sample []byte) float64 {
	var obj, total int
	for n := 0; n < 64 && len(sample) > 0; n++ {
		i := bytes.IndexByte(sample, '\n')
		var line []byte
		if i < 0 {
			line, sample = sample, nil
		} else {
			line, sample = sample[:i], sample[i+1:]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		total++
		if line[0] == '{' && line[len(line)-1] == '}' {
			obj++
		}
	}
	if total == 0 {
		return 0
	}
	return 0.95 * float64(obj) / float64(total)
}

// RegisterCore registers the always-on profiles: generic and JSON.
func RegisterCore(r *Registry) error {
	if err := r.Register(GenericProfile()); err != nil {
		return err
	}
	if err := r.Register(JSONProfile()); err != nil {
		return err
	}
	return r.Register(CRIProfile())
}
