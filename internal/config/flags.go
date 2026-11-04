// Command-line flags: registration, custom flag types and application to a Config.

package config

import (
	"errors"
	"flag"
	"fmt"
	"github.com/asyncpoint/logi/internal/filter"
	"io"
	"strconv"
	"strings"
)

// multiFlag collects a repeatable/comma separated flag.
type multiFlag struct{ set func(string) }

func (m multiFlag) String() string { return "" }

func (m multiFlag) Set(v string) error {
	for _, n := range splitList(v) {
		m.set(n)
	}
	return nil
}

// gutterFlag is a tri-state flag that still works as a bare switch:
// --source-gutter means on, --source-gutter=off disables, =auto restores the default.
type gutterFlag struct{ dst *string }

func (g gutterFlag) String() string {
	if g.dst == nil {
		return ""
	}
	return *g.dst
}

func (g gutterFlag) Set(v string) error {
	mode, err := ParseGutterMode(v)
	if err != nil {
		return err
	}
	*g.dst = mode
	return nil
}

func (g gutterFlag) IsBoolFlag() bool { return true }

type repeatFlag struct{ set func(string) }

func (r repeatFlag) String() string { return "" }

func (r repeatFlag) Set(v string) error { r.set(v); return nil }

type sizeFlag struct{ dst *int64 }

func (s sizeFlag) String() string {
	if s.dst == nil {
		return ""
	}
	return strconv.FormatInt(*s.dst, 10)
}

func (s sizeFlag) Set(v string) error {
	n, err := ParseSize(v)
	if err != nil {
		return err
	}
	*s.dst = n
	return nil
}

// flagLocals holds flag values that are folded into the Config after parsing.
type flagLocals struct {
	fromStart, noMulti                       bool
	maxRec                                   int64
	levels, contains, regex, fields, exclude []string
}

// flagDef declares one command-line flag, possibly with aliases, as data.
type flagDef struct {
	names []string // the first is the flag; the others are aliases of it
	usage string   // usage text of the first name; aliases are described as aliases
	bind  func(fs *flag.FlagSet, name, usage string)
}

func (d flagDef) register(fs *flag.FlagSet) {
	for i, n := range d.names {
		u := d.usage
		switch {
		case i == 0:
		case len(n) == 1:
			u = "short alias for --" + d.names[0]
		default:
			u = "alias for --" + d.names[0]
		}
		d.bind(fs, n, u)
	}
}

func boolDef(dst *bool, usage string, names ...string) flagDef {
	return flagDef{names, usage, func(fs *flag.FlagSet, n, u string) { fs.BoolVar(dst, n, *dst, u) }}
}

func stringDef(dst *string, usage string, names ...string) flagDef {
	return flagDef{names, usage, func(fs *flag.FlagSet, n, u string) { fs.StringVar(dst, n, *dst, u) }}
}

func intDef(dst *int, usage string, names ...string) flagDef {
	return flagDef{names, usage, func(fs *flag.FlagSet, n, u string) { fs.IntVar(dst, n, *dst, u) }}
}

// valueDef registers a flag with its own parsing (sizes, lists, tri-state...).
func valueDef(val flag.Value, usage string, names ...string) flagDef {
	return flagDef{names, usage, func(fs *flag.FlagSet, n, u string) { fs.Var(val, n, u) }}
}

// flagDefs is the table of every command-line flag, in the order --help-style
// documentation reads best. Adding a flag is one entry here (plus its --help text).
func flagDefs(c *Config, v *flagLocals) []flagDef {
	v.maxRec = int64(c.MaxRecordBytes)
	return []flagDef{
		stringDef(&c.ConfigFile, "path to a config file", "config"),
		boolDef(&c.ShowVersion, "print version and exit", "version", "V"),
		boolDef(&c.ListParsers, "list parser profiles and exit", "list-parsers"),
		valueDef(linesFlag{&c.Lines, &c.LinesSet}, "live mode: start with only the last N lines (implies --follow)", "lines", "n"),
		stringDef(&c.Since, "only records from this time on: a duration (15m, 2h, 1d) or a time (14:30, 2026-10-15 14:30)", "since"),
		stringDef(&c.Until, "only records up to this time (same formats as --since)", "until"),
		boolDef(&c.Follow, "live mode: follow the file as it grows (tail -f)", "follow", "live", "f"),
		boolDef(&v.fromStart, "live mode: read the file from the beginning instead of the last --backlog bytes", "from-start"),
		valueDef(sizeFlag{&c.Backlog}, "live mode: bytes before end-of-file to load at start (0 = only new data)", "backlog"),
		intDef(&c.Buffer, "live mode: ring buffer capacity in records", "buffer"),
		valueDef(sizeFlag{&c.BufferBytes}, "live mode: ring buffer byte budget (0 = unlimited)", "buffer-bytes"),
		stringDef(&c.Parser, "parser profile: auto, generic, json, ncache, ncache-events", "parser"),
		boolDef(&v.noMulti, "frame strictly one record per line", "no-multiline"),
		valueDef(multiFlag{func(n string) { c.Plugins[n] = false }}, "disable a built-in plugin (repeatable or comma separated)", "disable-plugin"),
		valueDef(multiFlag{func(n string) { c.Plugins[n] = true }}, "enable a built-in plugin", "enable-plugin"),
		valueDef(sizeFlag{&v.maxRec}, "truncate records larger than this", "max-record-size"),
		intDef(&c.MaxLines, "truncate multiline records beyond this many lines", "max-lines"),
		boolDef(&c.ForcePoll, "poll the file instead of using filesystem notifications", "poll"),
		stringDef(&c.Color, "colours: auto (detect), always (force 16-colour ANSI), never", "color"),
		stringDef(&c.Glob, "directory mode: file patterns, e.g. '*.log,*.txt' (default: all log-like files)", "glob"),
		stringDef(&c.DirMode, "directory mode: latest (follow the newest file, roll over) or all (follow every file)", "dir-mode"),
		intDef(&c.MaxFiles, "directory mode all: maximum files followed at once", "max-files"),
		valueDef(gutterFlag{&c.SourceGutter}, "directory mode all: file-colour bar in the row gutter: auto (default: shown while the source column is hidden), on, or off; write --source-gutter=off to disable", "source-gutter"),
		boolDef(&c.SourceMarkers, "directory mode all: with more than ten files, put a shape (◆ ▲ ■ ●) before the names whose colour repeats", "source-markers"),
		valueDef(multiFlag{func(n string) { c.HideColumns = append(c.HideColumns, n) }}, "columns to hide at start, e.g. id,source,thread (names depend on the log format)", "hide-columns"),
		valueDef(multiFlag{func(n string) { c.ShowColumns = append(c.ShowColumns, n) }}, "columns to force visible at start (e.g. source)", "show-columns"),
		stringDef(&c.Filter, "initial filter query; terms are ANDed, e.g. 'severity:>=warn message:timeout'", "filter"),
		valueDef(repeatFlag{func(x string) { v.levels = append(v.levels, x) }}, "startup severity filter (repeatable or comma separated, e.g. warn,error)", "level"),
		valueDef(repeatFlag{func(x string) { v.contains = append(v.contains, x) }}, "startup literal message/text filter (repeatable; terms are ANDed)", "contains"),
		valueDef(repeatFlag{func(x string) { v.regex = append(v.regex, x) }}, "startup regular-expression filter (repeatable; terms are ANDed)", "regex"),
		valueDef(repeatFlag{func(x string) { v.fields = append(v.fields, x) }}, "startup structured-field equality filter, e.g. host=server01 (repeatable)", "field"),
		valueDef(repeatFlag{func(x string) { v.exclude = append(v.exclude, x) }}, "exclude records containing this literal text (repeatable)", "exclude"),
		stringDef(&c.SearchMode, "default search mode: literal, regex, field", "search-mode"),
	}
}

// registerFlags defines every command-line flag. It is separate from parsing so
// tests can check that each flag is documented in --help.
func registerFlags(fs *flag.FlagSet, c *Config, v *flagLocals) {
	for _, d := range flagDefs(c, v) {
		d.register(fs)
	}
}

// dashed spells a flag name the way it is typed: -f, --follow.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// parseInterspersed parses args, allowing flags before and after the file
// arguments, and returns the positional arguments in order.
func parseInterspersed(fs *flag.FlagSet, args []string, c *Config, stderr io.Writer) ([]string, error) {
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				printHelp(stderr, c)
				return nil, err
			}
			return nil, explainParseError(err, fs)
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

// startupTerms are the sources of the startup filter, in the order their terms
// are appended to --filter and so the order problems are reported. Each turns
// the flags it owns into zero or more filter terms.
var startupTerms = []func(c *Config, v *flagLocals) ([]string, error){
	func(_ *Config, v *flagLocals) ([]string, error) { // --level a,b -> severity:a,b
		if len(v.levels) == 0 {
			return nil, nil
		}
		levels := make([]string, 0, len(v.levels))
		for _, x := range v.levels {
			levels = append(levels, strings.TrimSpace(x))
		}
		return []string{"severity:" + strings.Join(levels, ",")}, nil
	},
	quotedTerms(func(v *flagLocals) []string { return v.contains }, "", errQuote),
	quotedTerms(func(v *flagLocals) []string { return v.regex }, "re:", errQuote),
	quotedTerms(func(v *flagLocals) []string { return v.fields }, "", errors.New("--field values cannot contain a double quote")),
	quotedTerms(func(v *flagLocals) []string { return v.exclude }, "-", errQuote),
	timeTerm("since", func(c *Config) string { return c.Since }, false),
	timeTerm("until", func(c *Config) string { return c.Until }, true),
}

var errQuote = errors.New("startup filter values cannot contain a double quote; use --filter for advanced queries")

// quotedTerms turns each value into prefix+"value"; a value holding a double
// quote cannot be expressed in the filter language and is rejected with errBad.
func quotedTerms(values func(*flagLocals) []string, prefix string, errBad error) func(*Config, *flagLocals) ([]string, error) {
	return func(_ *Config, v *flagLocals) ([]string, error) {
		var out []string
		for _, x := range values(v) {
			if strings.ContainsRune(x, '"') {
				return nil, errBad
			}
			out = append(out, prefix+"\""+x+"\"")
		}
		return out, nil
	}
}

// timeTerm validates --since/--until and turns it into since:"..." / until:"...".
func timeTerm(name string, get func(*Config) string, until bool) func(*Config, *flagLocals) ([]string, error) {
	return func(c *Config, _ *flagLocals) ([]string, error) {
		val := get(c)
		if strings.TrimSpace(val) == "" {
			return nil, nil
		}
		if _, err := filter.ParseTimeSpec(val, until); err != nil {
			return nil, fmt.Errorf("--%s: %v", name, err)
		}
		val = strings.TrimSpace(val)
		if strings.ContainsRune(val, '"') {
			return nil, errQuote
		}
		return []string{name + ":\"" + val + "\""}, nil
	}
}

// foldStartupFilter appends the filter implied by --level, --contains, --regex,
// --field, --exclude, --since and --until to c.Filter.
func foldStartupFilter(c *Config, v *flagLocals) error {
	var terms []string
	for _, src := range startupTerms {
		t, err := src(c, v)
		if err != nil {
			return err
		}
		terms = append(terms, t...)
	}
	if len(terms) > 0 {
		if strings.TrimSpace(c.Filter) != "" {
			c.Filter += " "
		}
		c.Filter += strings.Join(terms, " ")
	}
	return nil
}

func applyFlags(c *Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("logi", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors and usage are printed by us, once, below
	fs.Usage = func() {}     // suppress flag.Parse's own automatic dump on every error
	var v flagLocals
	registerFlags(fs, c, &v)

	if err := validateDashes(args, fs); err != nil {
		return err
	}
	pos, err := parseInterspersed(fs, args, c, stderr)
	if err != nil {
		return err
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	// Flags that are folded into the Config, and the rules that tie flags together.
	c.MaxRecordBytes = int(v.maxRec)
	if v.fromStart {
		c.Backlog = -1
	}
	if v.noMulti {
		c.Multiline = "off"
	}
	if c.LinesSet {
		if v.fromStart || explicit["backlog"] {
			return errors.New("--lines cannot be combined with --from-start or --backlog: use one way of choosing how much history to load")
		}
		c.Follow = true
	}
	if err := resolvePaths(c, pos, explicit); err != nil {
		return err
	}
	return foldStartupFilter(c, &v)
}

// linesFlag is -n/--lines: a count of lines, 0 or more.
type linesFlag struct {
	dst *int
	set *bool
}

func (l linesFlag) String() string {
	if l.dst == nil {
		return ""
	}
	return strconv.Itoa(*l.dst)
}

func (l linesFlag) Set(v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return fmt.Errorf("%q is not a number of lines (0 or more)", v)
	}
	*l.dst, *l.set = n, true
	return nil
}
