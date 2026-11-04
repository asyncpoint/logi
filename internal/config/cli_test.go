package config

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, args ...string) (Config, error) {
	t.Helper()
	return Load(args, env(nil), io.Discard)
}

func mustLoad(t *testing.T, args ...string) Config {
	t.Helper()
	c, err := load(t, args...)
	if err != nil {
		t.Fatalf("logi %s: %v", strings.Join(args, " "), err)
	}
	return c
}

func errOf(t *testing.T, args ...string) string {
	t.Helper()
	_, err := load(t, args...)
	if err == nil {
		t.Fatalf("logi %s: expected an error", strings.Join(args, " "))
	}
	return err.Error()
}

func helpText(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	if _, err := Load([]string{"--help"}, env(nil), &out); err == nil {
		t.Fatal("--help should return the help sentinel")
	}
	return out.String()
}

// Every flag must appear in --help; a new flag cannot be forgotten.
func TestEveryFlagIsDocumentedInHelp(t *testing.T) {
	c := Defaults()
	var v flagLocals
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	registerFlags(fs, &c, &v)
	documented := map[string]bool{}
	for _, g := range helpGroups(&c) {
		for _, it := range g.items {
			for _, n := range it.names {
				documented[n] = true
			}
		}
	}
	fs.VisitAll(func(f *flag.Flag) {
		if !documented[f.Name] {
			t.Errorf("flag %q is registered but not described in --help", f.Name)
		}
	})
	for n := range documented {
		if fs.Lookup(n) == nil {
			t.Errorf("--help documents %q, which is not a registered flag", n)
		}
	}
}

func TestHelpIsGroupedAndReadable(t *testing.T) {
	h := helpText(t)
	for _, want := range []string{"Input:", "Live view:", "Directories:", "Filtering at start:", "Display:", "Parsing and limits:", "Other:",
		"logi [options] <file> <file> ...", "kubectl logs", "LOGI_FOLLOW", "NO_COLOR", "Config file", "--since <time>", "-n, --lines <n>", "-V, --version"} {
		if !strings.Contains(h, want) {
			t.Errorf("help is missing %q:\n%s", want, h)
		}
	}
	for _, raw := range []string{"8388608", "268435456", "65536"} {
		if strings.Contains(h, raw) {
			t.Errorf("help shows the raw number %s instead of a readable size", raw)
		}
	}
	for _, want := range []string{"8 MiB", "256 MiB", "64 KiB"} {
		if !strings.Contains(h, want) {
			t.Errorf("help should show the default as %q", want)
		}
	}
	if strings.Contains(h, "default auto) (default") || strings.Count(h, "(default auto)") < 1 {
		t.Error("defaults should be stated once")
	}
	for _, line := range strings.Split(h, "\n") {
		if len([]rune(line)) > 110 {
			t.Errorf("help line is too wide (%d columns): %q", len([]rune(line)), line)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 8 << 20: "8 MiB", 256 << 20: "256 MiB", 64 << 10: "64 KiB", 1 << 30: "1 GiB", 1536: "1.5 KiB", 500: "500 B", 3 << 19: "1536 KiB", 3<<20 + 7: "3.0 MiB"} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestVersionShortFlag(t *testing.T) {
	if !mustLoad(t, "-V").ShowVersion || !mustLoad(t, "--version").ShowVersion {
		t.Fatal("-V and --version should both request the version")
	}
}

func TestLinesFlag(t *testing.T) {
	c := mustLoad(t, "-n", "500", "app.log")
	if !c.LinesSet || c.Lines != 500 || !c.Follow {
		t.Fatalf("-n 500 should follow with the last 500 lines: %+v", c)
	}
	if c := mustLoad(t, "--lines", "0", "app.log"); !c.LinesSet || c.Lines != 0 || !c.Follow {
		t.Fatalf("--lines 0 means only new lines: %+v", c)
	}
	if mustLoad(t, "app.log").LinesSet {
		t.Fatal("lines must be unset by default")
	}
	for _, bad := range []string{"-5", "many", "1.5"} {
		if msg := errOf(t, "-n", bad, "app.log"); !strings.Contains(msg, "not a number of lines") {
			t.Errorf("-n %s: unhelpful error %q", bad, msg)
		}
	}
	if msg := errOf(t, "-n", "10", "--backlog", "1MiB", "app.log"); !strings.Contains(msg, "--lines cannot be combined") {
		t.Errorf("--lines with --backlog: %q", msg)
	}
	if msg := errOf(t, "-n", "10", "--from-start", "app.log"); !strings.Contains(msg, "--lines cannot be combined") {
		t.Errorf("--lines with --from-start: %q", msg)
	}
}

func TestSinceAndUntilJoinTheFilter(t *testing.T) {
	c := mustLoad(t, "--since", "15m", "--until", "2026-10-15 14:30", "--level", "error", "app.log")
	for _, want := range []string{`since:"15m"`, `until:"2026-10-15 14:30"`, "severity:error"} {
		if !strings.Contains(c.Filter, want) {
			t.Errorf("filter %q should contain %s", c.Filter, want)
		}
	}
	if msg := errOf(t, "--since", "15x", "app.log"); !strings.Contains(msg, "--since") || !strings.Contains(msg, "15m") {
		t.Errorf("a bad --since should explain the accepted forms: %q", msg)
	}
	if msg := errOf(t, "--until", "tomorrow", "app.log"); !strings.Contains(msg, "--until") {
		t.Errorf("a bad --until should name the flag: %q", msg)
	}
}

func writeFiles(t *testing.T, dir string, names ...string) []string {
	t.Helper()
	var out []string
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("2026-10-15 10:00:00 INFO x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestSeveralFilesFollowTogether(t *testing.T) {
	dir := t.TempDir()
	f := writeFiles(t, dir, "api.log", "web.log", "worker.log")
	c := mustLoad(t, f[0], f[1], f[2])
	if c.Path != filepath.Clean(dir) || c.DirMode != "all" || !c.Follow || c.Glob != "api.log,web.log,worker.log" || c.Stdin {
		t.Fatalf("several files should become a merged live view of the directory: %+v", c)
	}
	// flags may be mixed in, and a repeated file is listed once
	c = mustLoad(t, f[0], "--level", "error", f[1], f[0])
	if c.Glob != "api.log,web.log" || !strings.Contains(c.Filter, "severity:error") {
		t.Fatalf("got glob %q filter %q", c.Glob, c.Filter)
	}
	// more files than --max-files raises the limit rather than silently dropping some
	many := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, writeFiles(t, dir, "m"+string(rune('a'+i%26))+string(rune('a'+i/26))+".log")...)
	}
	if c := mustLoad(t, many...); c.MaxFiles < 40 {
		t.Fatalf("40 files must all be followed, MaxFiles=%d", c.MaxFiles)
	}
}

func TestSeveralFilesMistakes(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	a := writeFiles(t, dir, "a.log")[0]
	b := writeFiles(t, other, "b.log")[0]
	if msg := errOf(t, a, b); !strings.Contains(msg, "same directory") {
		t.Errorf("files from two directories: %q", msg)
	}
	if msg := errOf(t, a, dir); !strings.Contains(msg, "is a directory") {
		t.Errorf("a file and a directory: %q", msg)
	}
	if msg := errOf(t, a, filepath.Join(dir, "nope.log")); !strings.Contains(msg, "cannot find") || !strings.Contains(msg, "nope.log") {
		t.Errorf("a missing file should be named: %q", msg)
	}
	c := writeFiles(t, dir, "c.log")[0]
	if msg := errOf(t, "--glob", "*.log", a, c); !strings.Contains(msg, "--glob cannot be combined") {
		t.Errorf("--glob with several files: %q", msg)
	}
	comma := writeFiles(t, dir, "x,y.log")[0]
	if msg := errOf(t, a, comma); !strings.Contains(msg, "comma") {
		t.Errorf("a comma in a name: %q", msg)
	}
}

func TestWildcardsAreExpandedWhenTheShellDoesNot(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "one.log", "two.log", "skip.txt")
	c := mustLoad(t, filepath.Join(dir, "*.log"))
	if c.Glob != "one.log,two.log" || c.DirMode != "all" || !c.Follow {
		t.Fatalf("a wildcard should expand to the matching files: %+v", c)
	}
	only := t.TempDir()
	writeFiles(t, only, "solo.log")
	if c := mustLoad(t, filepath.Join(only, "*.log")); c.Path != filepath.Join(only, "solo.log") || c.Glob != "" {
		t.Fatalf("a wildcard matching one file is just that file: %+v", c)
	}
	if msg := errOf(t, filepath.Join(dir, "*.nope")); !strings.Contains(msg, "no files match") {
		t.Errorf("a wildcard matching nothing: %q", msg)
	}
	// a file that really has a bracket in its name is used as is
	odd := writeFiles(t, dir, "weird[1].log")[0]
	if c := mustLoad(t, odd); c.Path != odd {
		t.Fatalf("a literal file name must win over wildcard expansion: %+v", c)
	}
	// names with wildcard characters match literally in a multi-file view
	if c := mustLoad(t, odd, filepath.Join(dir, "one.log")); c.Glob != "weird[[]1].log,one.log" {
		t.Fatalf("wildcard characters in names must be escaped, got %q", c.Glob)
	}
}

func TestStandardInput(t *testing.T) {
	c := mustLoad(t, "-")
	if !c.Stdin || !c.Follow || c.Path != "" {
		t.Fatalf("- reads standard input live: %+v", c)
	}
	if msg := errOf(t, "-", "a.log"); !strings.Contains(msg, "standard input") {
		t.Errorf("- with files: %q", msg)
	}
	if mustLoad(t, "app.log").Stdin {
		t.Fatal("a file argument is not stdin")
	}
}

func TestChoicesAreCaseInsensitiveWithSuggestions(t *testing.T) {
	c := mustLoad(t, "--dir-mode", "ALL", "--color", "Always", "--search-mode", "REGEX", "--parser", "JSON", "logs/")
	if c.DirMode != "all" || c.Color != "always" || c.SearchMode != "regex" || c.Parser != "json" {
		t.Fatalf("choices should be matched ignoring case and normalised: %+v", c)
	}
	if msg := errOf(t, "--dir-mode", "al", "logs/"); !strings.Contains(msg, `did you mean "all"`) || !strings.Contains(msg, "latest or all") {
		t.Errorf("--dir-mode al: %q", msg)
	}
	if msg := errOf(t, "--color", "blue", "app.log"); !strings.Contains(msg, "auto, always or never") || strings.Contains(msg, "did you mean") {
		t.Errorf("--color blue should list the choices without a bogus suggestion: %q", msg)
	}
	if msg := errOf(t, "--search-mode", "regx", "app.log"); !strings.Contains(msg, `did you mean "regex"`) {
		t.Errorf("--search-mode regx: %q", msg)
	}
}

func TestFlagErrorsAreSpecific(t *testing.T) {
	if msg := errOf(t, "-v", "app.log"); !strings.Contains(msg, "unrecognized flag -v") || strings.Contains(msg, "did you mean") || strings.Contains(msg, "--v") {
		t.Errorf("-v: %q", msg)
	}
	if msg := errOf(t, "--folow", "app.log"); !strings.Contains(msg, "did you mean --follow") {
		t.Errorf("--folow: %q", msg)
	}
	if msg := errOf(t, "--buffer", "lots", "app.log"); !strings.Contains(msg, `invalid value "lots" for --buffer`) || !strings.Contains(msg, "not a valid number") || strings.Contains(msg, "flag -buffer") {
		t.Errorf("--buffer lots: %q", msg)
	}
	if msg := errOf(t, "app.log", "--level"); !strings.Contains(msg, "--level needs a value") {
		t.Errorf("--level without a value: %q", msg)
	}
	if msg := errOf(t, "--backlog", "10XB", "app.log"); !strings.Contains(msg, "--backlog") || !strings.Contains(msg, "size") {
		t.Errorf("--backlog 10XB: %q", msg)
	}
}

func TestPathErrorExplainsItself(t *testing.T) {
	dir := t.TempDir()
	_, err := os.Stat(filepath.Join(dir, "missing.log"))
	if msg := PathError("missing.log", err).Error(); !strings.Contains(msg, `cannot find "missing.log"`) || strings.Contains(msg, "stat ") {
		t.Errorf("missing file: %q", msg)
	}
}
