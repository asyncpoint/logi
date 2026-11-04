package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/filter"
)

// The original hand-written flag registration and applyFlags, kept as the
// specification the table-driven versions must agree with.
func registerFlagsRef(fs *flag.FlagSet, c *Config, v *flagLocals) {
	v.maxRec = int64(c.MaxRecordBytes)
	fs.StringVar(&c.ConfigFile, "config", c.ConfigFile, "path to a config file")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")
	fs.BoolVar(&c.ShowVersion, "V", false, "short alias for --version")
	fs.BoolVar(&c.ListParsers, "list-parsers", false, "list parser profiles and exit")
	fs.Var(linesFlag{&c.Lines, &c.LinesSet}, "lines", "live mode: start with only the last N lines (implies --follow)")
	fs.Var(linesFlag{&c.Lines, &c.LinesSet}, "n", "short alias for --lines")
	fs.StringVar(&c.Since, "since", "", "only records from this time on: a duration (15m, 2h, 1d) or a time (14:30, 2026-10-15 14:30)")
	fs.StringVar(&c.Until, "until", "", "only records up to this time (same formats as --since)")
	fs.BoolVar(&c.Follow, "follow", c.Follow, "live mode: follow the file as it grows (tail -f)")
	fs.BoolVar(&c.Follow, "live", c.Follow, "alias for --follow")
	fs.BoolVar(&c.Follow, "f", c.Follow, "short alias for --follow")
	fs.BoolVar(&v.fromStart, "from-start", false, "live mode: read the file from the beginning instead of the last --backlog bytes")
	fs.Var(sizeFlag{&c.Backlog}, "backlog", "live mode: bytes before end-of-file to load at start (0 = only new data)")
	fs.IntVar(&c.Buffer, "buffer", c.Buffer, "live mode: ring buffer capacity in records")
	fs.Var(sizeFlag{&c.BufferBytes}, "buffer-bytes", "live mode: ring buffer byte budget (0 = unlimited)")
	fs.StringVar(&c.Parser, "parser", c.Parser, "parser profile: auto, generic, json, ncache, ncache-events")
	fs.BoolVar(&v.noMulti, "no-multiline", false, "frame strictly one record per line")
	fs.Var(multiFlag{func(n string) { c.Plugins[n] = false }}, "disable-plugin", "disable a built-in plugin (repeatable or comma separated)")
	fs.Var(multiFlag{func(n string) { c.Plugins[n] = true }}, "enable-plugin", "enable a built-in plugin")
	fs.Var(sizeFlag{&v.maxRec}, "max-record-size", "truncate records larger than this")
	fs.IntVar(&c.MaxLines, "max-lines", c.MaxLines, "truncate multiline records beyond this many lines")
	fs.BoolVar(&c.ForcePoll, "poll", c.ForcePoll, "poll the file instead of using filesystem notifications")
	fs.StringVar(&c.Color, "color", c.Color, "colours: auto (detect), always (force 16-colour ANSI), never")
	fs.StringVar(&c.Glob, "glob", c.Glob, "directory mode: file patterns, e.g. '*.log,*.txt' (default: all log-like files)")
	fs.StringVar(&c.DirMode, "dir-mode", c.DirMode, "directory mode: latest (follow the newest file, roll over) or all (follow every file)")
	fs.IntVar(&c.MaxFiles, "max-files", c.MaxFiles, "directory mode all: maximum files followed at once")
	fs.Var(gutterFlag{&c.SourceGutter}, "source-gutter", "directory mode all: file-colour bar in the row gutter: auto (default: shown while the source column is hidden), on, or off; write --source-gutter=off to disable")
	fs.BoolVar(&c.SourceMarkers, "source-markers", c.SourceMarkers, "directory mode all: with more than ten files, put a shape (◆ ▲ ■ ●) before the names whose colour repeats")
	fs.Var(multiFlag{func(n string) { c.HideColumns = append(c.HideColumns, n) }}, "hide-columns", "columns to hide at start, e.g. id,source,thread (names depend on the log format)")
	fs.Var(multiFlag{func(n string) { c.ShowColumns = append(c.ShowColumns, n) }}, "show-columns", "columns to force visible at start (e.g. source)")
	fs.StringVar(&c.Filter, "filter", c.Filter, "initial filter query; terms are ANDed, e.g. 'severity:>=warn message:timeout'")
	fs.Var(repeatFlag{func(x string) { v.levels = append(v.levels, x) }}, "level", "startup severity filter (repeatable or comma separated, e.g. warn,error)")
	fs.Var(repeatFlag{func(x string) { v.contains = append(v.contains, x) }}, "contains", "startup literal message/text filter (repeatable; terms are ANDed)")
	fs.Var(repeatFlag{func(x string) { v.regex = append(v.regex, x) }}, "regex", "startup regular-expression filter (repeatable; terms are ANDed)")
	fs.Var(repeatFlag{func(x string) { v.fields = append(v.fields, x) }}, "field", "startup structured-field equality filter, e.g. host=server01 (repeatable)")
	fs.Var(repeatFlag{func(x string) { v.exclude = append(v.exclude, x) }}, "exclude", "exclude records containing this literal text (repeatable)")
	fs.StringVar(&c.SearchMode, "search-mode", c.SearchMode, "default search mode: literal, regex, field")
}

func applyFlagsRef(c *Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("logi", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors and usage are printed by us, once, below
	fs.Usage = func() {}     // suppress flag.Parse's own automatic dump on every error
	var v flagLocals
	registerFlagsRef(fs, c, &v)

	if err := validateDashes(args, fs); err != nil {
		return err
	}

	// Allow flags before *and* after the file argument.
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				printHelp(stderr, c)
				return err
			}
			return explainParseError(err, fs)
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

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

	terms := make([]string, 0, len(v.levels)+len(v.contains)+len(v.regex)+len(v.fields)+len(v.exclude)+2)
	if len(v.levels) > 0 {
		levels := make([]string, 0, len(v.levels))
		for _, x := range v.levels {
			levels = append(levels, strings.TrimSpace(x))
		}
		terms = append(terms, "severity:"+strings.Join(levels, ","))
	}
	quoteTerm := func(x string) (string, error) {
		if strings.ContainsRune(x, '"') {
			return "", errors.New("startup filter values cannot contain a double quote; use --filter for advanced queries")
		}
		return "\"" + x + "\"", nil
	}
	for _, x := range v.contains {
		q, err := quoteTerm(x)
		if err != nil {
			return err
		}
		terms = append(terms, q)
	}
	for _, x := range v.regex {
		q, err := quoteTerm(x)
		if err != nil {
			return err
		}
		terms = append(terms, "re:"+q)
	}
	for _, x := range v.fields {
		if strings.ContainsRune(x, '"') {
			return errors.New("--field values cannot contain a double quote")
		}
		terms = append(terms, "\""+x+"\"")
	}
	for _, x := range v.exclude {
		q, err := quoteTerm(x)
		if err != nil {
			return err
		}
		terms = append(terms, "-"+q)
	}
	for _, w := range []struct {
		key, val string
		until    bool
	}{{"since", c.Since, false}, {"until", c.Until, true}} {
		if strings.TrimSpace(w.val) == "" {
			continue
		}
		if _, err := filter.ParseTimeSpec(w.val, w.until); err != nil {
			return fmt.Errorf("--%s: %v", w.key, err)
		}
		q, err := quoteTerm(strings.TrimSpace(w.val))
		if err != nil {
			return err
		}
		terms = append(terms, w.key+":"+q)
	}
	if len(terms) > 0 {
		if strings.TrimSpace(c.Filter) != "" {
			c.Filter += " "
		}
		c.Filter += strings.Join(terms, " ")
	}
	return nil
}

var _ = filter.ParseTimeSpec

func TestFlagTableMatchesReferenceRegistration(t *testing.T) {
	type meta struct{ usage, def string }
	collect := func(register func(*flag.FlagSet, *Config, *flagLocals)) map[string]meta {
		c := Defaults()
		var v flagLocals
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		register(fs, &c, &v)
		out := map[string]meta{}
		fs.VisitAll(func(f *flag.Flag) { out[f.Name] = meta{f.Usage, f.DefValue} })
		return out
	}
	got, want := collect(registerFlags), collect(registerFlagsRef)
	if !reflect.DeepEqual(got, want) {
		for n, w := range want {
			if g, ok := got[n]; !ok || g != w {
				t.Errorf("flag %q: got %+v, want %+v", n, g, w)
			}
		}
		for n := range got {
			if _, ok := want[n]; !ok {
				t.Errorf("flag %q is registered but the reference lacks it", n)
			}
		}
	}
}

func TestFlagDefsHaveNoDuplicateNames(t *testing.T) {
	c := Defaults()
	var v flagLocals
	seen := map[string]bool{}
	for _, d := range flagDefs(&c, &v) {
		if len(d.names) == 0 || d.bind == nil || d.usage == "" {
			t.Errorf("incomplete flag definition %+v", d.names)
		}
		for _, n := range d.names {
			if seen[n] {
				t.Errorf("flag %q defined twice", n)
			}
			seen[n] = true
		}
	}
}

var flagPools = map[string][]string{
	"size":   {"0", "1MiB", "64KiB", "x", "-1", "8MB", ""},
	"int":    {"5", "0", "-1", "x", "20000"},
	"lines":  {"5", "0", "-2", "x", " 7 "},
	"gutter": {"on", "off", "auto", "bad"},
	"time":   {"15m", "2h", "14:30", "2026-10-15 14:30", "bad", "", "a\"b", " 1d "},
	"list":   {"warn", "warn,error", " info ", "", "id,source"},
	"text":   {"timeout", "a\"b", "host=x", "x y", "", "severity:>=warn"},
	"plugin": {"ncache", "a,b", ""},
	"mode":   {"always", "never", "auto", "bad", "latest", "all", "literal", "regex", "field", "json", "*.log", "x"},
}

var flagKinds = map[string]string{
	"backlog": "size", "buffer-bytes": "size", "max-record-size": "size",
	"buffer": "int", "max-lines": "int", "max-files": "int",
	"lines": "lines", "n": "lines", "source-gutter": "gutter",
	"since": "time", "until": "time", "level": "list", "hide-columns": "list", "show-columns": "list",
	"contains": "text", "regex": "text", "field": "text", "exclude": "text", "filter": "text",
	"enable-plugin": "plugin", "disable-plugin": "plugin",
	"color": "mode", "parser": "mode", "glob": "mode", "dir-mode": "mode", "search-mode": "mode", "config": "mode",
}

var boolFlags = map[string]bool{"version": true, "V": true, "list-parsers": true, "follow": true, "live": true, "f": true,
	"from-start": true, "no-multiline": true, "poll": true, "source-markers": true}

func randomArgs(rng *rand.Rand, names []string, dir string) []string {
	var args []string
	for n := rng.Intn(7); n > 0; n-- {
		switch rng.Intn(12) {
		case 0:
			args = append(args, []string{filepath.Join(dir, "a.log"), dir, filepath.Join(dir, "*.log"), filepath.Join(dir, "missing.log"), "no-such-file.log"}[rng.Intn(5)])
		case 1:
			args = append(args, []string{"--bogus", "-follow", "--buffr=3", "--help", "-h", "--", "-", "---x", "--dir-mod=all", "-dir-mode=all"}[rng.Intn(10)])
		default:
			name := names[rng.Intn(len(names))]
			dash := "--"
			if len(name) == 1 {
				dash = "-"
			}
			if boolFlags[name] {
				if rng.Intn(3) == 0 {
					args = append(args, dash+name+"="+[]string{"true", "false", "maybe", "1"}[rng.Intn(4)])
				} else {
					args = append(args, dash+name)
				}
				continue
			}
			if name == "source-gutter" && rng.Intn(3) == 0 {
				args = append(args, dash+name) // bare form means "on"
				continue
			}
			pool := flagPools[flagKinds[name]]
			if pool == nil {
				pool = flagPools["text"]
			}
			val := pool[rng.Intn(len(pool))]
			if rng.Intn(2) == 0 {
				args = append(args, dash+name+"="+val)
			} else {
				args = append(args, dash+name, val)
			}
		}
	}
	return args
}

func TestApplyFlagsMatchesReference(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := Defaults()
	var v flagLocals
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	registerFlagsRef(fs, &probe, &v)
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })

	rng := rand.New(rand.NewSource(11))
	n := 15000
	if testing.Short() { // make short: a tenth of the cases
		n = 1500
	}
	for i := 0; i < n; i++ {
		args := randomArgs(rng, names, dir)
		mk := func() Config {
			c := Defaults()
			switch i % 4 { // also start from non-default values, as env/file would set them
			case 1:
				c.Follow, c.Buffer = true, 123
			case 2:
				c.Color, c.Filter = "never", "level:error"
			}
			return c
		}
		a, b := mk(), mk()
		var outA, outB strings.Builder
		errA := applyFlags(&a, args, &outA)
		errB := applyFlagsRef(&b, args, &outB)
		if fmt.Sprint(errA) != fmt.Sprint(errB) || errors.Is(errA, flag.ErrHelp) != errors.Is(errB, flag.ErrHelp) {
			t.Fatalf("args %q:\n error %v\n want  %v", args, errA, errB)
		}
		if outA.String() != outB.String() {
			t.Fatalf("args %q: help output differs", args)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("args %q: config differs\n got %+v\nwant %+v", args, a, b)
		}
	}
}
