package config

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asyncpoint/logi/internal/filter"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := Load([]string{"app.log"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Path != "app.log" || c.Follow || c.Buffer != 50000 || c.Parser != "auto" || c.Backlog != 8<<20 {
		t.Fatalf("%+v", c)
	}
}

func TestPrecedenceCLIOverEnvOverFileOverDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "c.yaml")
	os.WriteFile(cfgFile, []byte(`
# logi config
buffer:
  capacity: 1000
  max_bytes: 64MiB
plugins:
  ncache:
    enabled: false
performance:
  batch_interval: 50ms
parser: json
keys:
  search: "ctrl+f, /"
`), 0o644)
	// file only
	c, err := Load([]string{"--config", cfgFile, "a.log"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Buffer != 1000 || c.BufferBytes != 64<<20 || c.Plugins["ncache"] != false || c.BatchInterval != 50*time.Millisecond || c.Parser != "json" {
		t.Fatalf("file: %+v", c)
	}
	if keys := c.Keys["search"]; len(keys) != 2 || keys[1] != "/" {
		t.Fatalf("keys %v", keys)
	}
	if v, ok := c.Plugins["ncache"]; !ok || v {
		t.Fatal("plugin disable from file")
	}
	// env beats file
	c, _ = Load([]string{"--config", cfgFile, "a.log"}, env(map[string]string{"LOGI_BUFFER": "2000"}), io.Discard)
	if c.Buffer != 2000 {
		t.Fatalf("env: %d", c.Buffer)
	}
	// CLI beats env; flags may follow the file
	c, _ = Load([]string{"--config", cfgFile, "a.log", "--buffer", "3000", "-f", "--enable-plugin", "ncache"}, env(map[string]string{"LOGI_BUFFER": "2000"}), io.Discard)
	if c.Buffer != 3000 || !c.Follow || !c.Plugins["ncache"] {
		t.Fatalf("cli: %+v", c)
	}
}

func TestFollowAliasesAndHelpOptions(t *testing.T) {
	for _, args := range [][]string{{"--follow", "app.log"}, {"--live", "app.log"}, {"-f", "app.log"}} {
		c, err := Load(args, env(nil), io.Discard)
		if err != nil || !c.Follow {
			t.Errorf("follow option %v: Follow=%v err=%v", args[0], c.Follow, err)
		}
	}

	var output strings.Builder
	_, err := Load([]string{"--help"}, env(nil), &output)
	if err == nil {
		t.Fatal("--help should return the help sentinel")
	}
	help := output.String()
	for _, want := range []string{"--follow", "--live", "-f", "--parser", "--disable-plugin", "--hide-columns", "--buffer-bytes", "--dir-mode", "--level", "--contains", "--regex", "--field", "--exclude"} {
		if !strings.Contains(help, want) {
			t.Errorf("help output does not include %q; output was:\n%s", want, help)
		}
	}
	options := strings.SplitN(help, "Examples:", 2)[0]
	for _, want := range []string{"--follow", "--parser", "--disable-plugin", "--hide-columns", "--buffer-bytes", "--dir-mode", "--level", "--contains", "--regex", "--field", "--exclude"} {
		if !strings.Contains(options, want) {
			t.Errorf("help does not display %q using long-option syntax; options were:\n%s", want, options)
		}
	}
	for _, line := range strings.Split(options, "\n") {
		if strings.HasPrefix(line, "  -follow") || strings.HasPrefix(line, "  -parser") {
			t.Errorf("Options section uses single-dash long options:\n%s", options)
			break
		}
	}
}

func TestStartupFilterFlagsComposeWithFilter(t *testing.T) {
	c, err := Load([]string{
		"--filter", "source:ncache", "--level", "warn,error", "--level", "fatal",
		"--contains", "connection refused", "--regex", `timeout\s+\d+`,
		"--field", "host=server01", "--exclude", "health check", "app.log", "--follow",
	}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Follow {
		t.Fatal("follow flag after path was not applied")
	}
	for _, want := range []string{"source:ncache", "severity:warn,error,fatal", `"connection refused"`, `re:"timeout\s+\d+"`, `"host=server01"`, `-"health check"`} {
		if !strings.Contains(c.Filter, want) {
			t.Errorf("combined filter %q does not contain %q", c.Filter, want)
		}
	}
	if _, err := filter.Compile(c.Filter); err != nil {
		t.Fatalf("combined startup query should compile: %v", err)
	}
}

func TestFlagsAndSizes(t *testing.T) {
	c, err := Load([]string{"--from-start", "--no-multiline", "--disable-plugin", "ncache,foo", "--max-record-size", "1MiB", "x.log"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Backlog != -1 || c.Multiline != "off" || c.Plugins["ncache"] || c.Plugins["foo"] || c.MaxRecordBytes != 1<<20 {
		t.Fatalf("%+v", c)
	}
	for in, want := range map[string]int64{"64k": 64 << 10, "8MiB": 8 << 20, "1G": 1 << 30, "123": 123} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("%s -> %d %v", in, got, err)
		}
	}
	if _, err := ParseSize("abc"); err == nil {
		t.Fatal("bad size")
	}
}

func TestValidationAndErrors(t *testing.T) {
	if _, err := Load([]string{"--buffer", "0", "a"}, env(nil), io.Discard); err == nil {
		t.Fatal("buffer 0 must fail")
	}
	if _, err := Load([]string{"a", "b"}, env(nil), io.Discard); err == nil {
		t.Fatal("two files must fail")
	}
	if _, err := Load([]string{"--config", "/nonexistent/x.yaml", "a"}, env(nil), io.Discard); err == nil {
		t.Fatal("explicit missing config must fail")
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(bad, []byte("buffer:\n\tcapacity: 5\n"), 0o644)
	if _, err := Load([]string{"--config", bad, "a"}, env(nil), io.Discard); err == nil {
		t.Fatal("tab indentation must fail")
	}
}

func TestColorDirectoryAndColumnOptions(t *testing.T) {
	c, err := Load([]string{"--color", "always", "--glob", "*.log,*.txt", "--dir-mode", "all", "--max-files", "8",
		"--hide-columns", "id,source", "--show-columns", "time", "logs/"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Color != "always" || c.Glob != "*.log,*.txt" || c.DirMode != "all" || c.MaxFiles != 8 ||
		len(c.HideColumns) != 2 || c.HideColumns[1] != "source" || c.ShowColumns[0] != "time" {
		t.Fatalf("%+v", c)
	}
	if _, err := Load([]string{"--hide-columns", "thread,logger,appdomain", "x"}, env(nil), io.Discard); err != nil {
		t.Fatalf("format-specific column names must be accepted: %v", err)
	}
	d, _ := Load([]string{"x"}, env(map[string]string{"LOGI_COLOR": "never"}), io.Discard)
	if d.Color != "never" || d.DirMode != "latest" || d.MaxFiles != 32 {
		t.Fatalf("defaults/env: %+v", d)
	}
	for _, bad := range [][]string{{"--color", "rainbow", "x"}, {"--dir-mode", "some", "x"}, {"--hide-columns", "Bad Name!", "x"}, {"--max-files", "0", "x"}} {
		if _, err := Load(bad, env(nil), io.Discard); err == nil {
			t.Errorf("expected error for %v", bad)
		}
	}
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte("color: never\ndirectory:\n  mode: all\n  glob: \"*.log\"\n  max_files: 4\ncolumns:\n  hidden: \"id, source\"\n  shown: time\n"), 0o644)
	e, err := Load([]string{"--config", f, "x"}, env(nil), io.Discard)
	if err != nil || e.Color != "never" || e.DirMode != "all" || e.Glob != "*.log" || e.MaxFiles != 4 ||
		len(e.HideColumns) != 2 || e.ShowColumns[0] != "time" {
		t.Fatalf("file: %+v %v", e, err)
	}
}

func TestDashConventionRejectsSingleDashLongFlags(t *testing.T) {
	// The exact scenario reported: a typo with two dashes still gets a helpful
	// suggestion instead of Go's confusing single-dash-rendered message.
	_, err := Load([]string{"-f", "--dir-mod", "all", "d:\\docker\\nclogs"}, env(nil), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--dir-mode") {
		t.Fatalf("expected a suggestion for --dir-mod, got: %v", err)
	}
	// A long flag name written with a single dash must be rejected, even
	// though it names a real flag — previously flag.Parse accepted this silently.
	_, err = Load([]string{"-f", "-dir-mode", "all", "logs/"}, env(nil), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "-dir-mode") || !strings.Contains(err.Error(), "--dir-mode") {
		t.Fatalf("single-dash long flag must be rejected with a --dir-mode suggestion, got: %v", err)
	}
	for _, bad := range []string{"-buffer", "-follow", "-no-multiline", "-max-files"} {
		if _, err := Load([]string{bad, "10", "a.log"}, env(nil), io.Discard); err == nil {
			t.Errorf("%q (single dash, multi-char) must be rejected", bad)
		}
	}
}

func TestDashConventionSuggestsClosestFlagForTypos(t *testing.T) {
	cases := map[string]string{
		"--bufer":      "--buffer",
		"--folow":      "--follow",
		"--dir-modee":  "--dir-mode",
		"--max-file":   "--max-files",
		"--serch-mode": "--search-mode",
	}
	for bad, want := range cases {
		_, err := Load([]string{bad, "x", "a.log"}, env(nil), io.Discard)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: expected suggestion %s, got: %v", bad, want, err)
		}
	}
	// A typo with nothing close enough gets a plain message, not a wrong guess.
	_, err := Load([]string{"--zzzzzzzzzz", "a.log"}, env(nil), io.Discard)
	if err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("an unrelated flag name should not get a spurious suggestion: %v", err)
	}
}

func TestDashConventionLeavesShortAndCorrectFlagsAlone(t *testing.T) {
	// Single-character flags with one dash are fine, exactly as before.
	if _, err := Load([]string{"-f", "a.log"}, env(nil), io.Discard); err != nil {
		t.Fatalf("-f must still work: %v", err)
	}
	// Correct double-dash long flags are fine.
	c, err := Load([]string{"--dir-mode", "all", "--buffer", "1000", "logs/"}, env(nil), io.Discard)
	if err != nil || c.DirMode != "all" || c.Buffer != 1000 {
		t.Fatalf("correctly-dashed flags must work: %+v %v", c, err)
	}
	// The "=" form must still be recognised as the same flag name.
	if _, err := Load([]string{"--buffer=1000", "a.log"}, env(nil), io.Discard); err != nil {
		t.Fatalf("--flag=value form must work: %v", err)
	}
	// A literal "--" still ends flag parsing, same as Go's flag package.
	c, err = Load([]string{"--", "-oddly-named-file.log"}, env(nil), io.Discard)
	if err != nil || c.Path != "-oddly-named-file.log" {
		t.Fatalf("-- must still end flag parsing: %+v %v", c, err)
	}
}

func TestEditDistanceAndClosestFlagName(t *testing.T) {
	if editDistance("buffer", "buffer") != 0 {
		t.Fatal("identical strings")
	}
	if editDistance("bufer", "buffer") != 1 {
		t.Fatal("one insertion")
	}
	if editDistance("", "abc") != 3 || editDistance("abc", "") != 3 {
		t.Fatal("empty string")
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("buffer", "", "")
	fs.String("follow", "", "")
	fs.String("dir-mode", "", "")
	if got := closestFlagName("bufer", fs); got != "buffer" {
		t.Fatalf("closestFlagName(bufer) = %q", got)
	}
	if got := closestFlagName("completely-unrelated-name", fs); got != "" {
		t.Fatalf("a distant name must not get a suggestion, got %q", got)
	}
}

func TestValidateRequiresPositiveFilterMatchCap(t *testing.T) {
	c := Defaults()
	c.MaxFilterMatches = 0
	if err := c.Validate(); err == nil {
		t.Fatal("zero filter match cap must be rejected")
	}
}

func TestSourceMarkersAreOffByDefaultAndSwitchable(t *testing.T) {
	c, err := Load([]string{"--dir-mode", "all", "logs/"}, env(nil), io.Discard)
	if err != nil || c.SourceMarkers {
		t.Fatalf("source markers must default to off: %+v, %v", c, err)
	}
	c, err = Load([]string{"--dir-mode", "all", "--source-markers", "logs/"}, env(nil), io.Discard)
	if err != nil || !c.SourceMarkers {
		t.Fatalf("--source-markers must turn them on: %+v, %v", c, err)
	}
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte("directory:\n  mode: all\n  source_markers: true\n"), 0o644)
	c, err = Load([]string{"--config", f, "logs/"}, env(nil), io.Discard)
	if err != nil || !c.SourceMarkers {
		t.Fatalf("directory.source_markers: true in the config file must turn them on: %+v, %v", c, err)
	}
	c, err = Load([]string{"--config", f, "--source-markers=false", "logs/"}, env(nil), io.Discard)
	if err != nil || c.SourceMarkers {
		t.Fatalf("the flag must override the config file: %+v, %v", c, err)
	}
}

func TestSourceGutterIsAutoByDefaultAndTriState(t *testing.T) {
	c, err := Load([]string{"--dir-mode", "all", "logs/"}, env(nil), io.Discard)
	if err != nil || c.SourceGutter != "auto" {
		t.Fatalf("source gutter must default to auto: %+v, %v", c, err)
	}
	for args, want := range map[string]string{
		"--source-gutter":       "on", // a bare switch still works
		"--source-gutter=on":    "on",
		"--source-gutter=off":   "off",
		"--source-gutter=false": "off",
		"--source-gutter=auto":  "auto",
		"--source-gutter=TRUE":  "on",
	} {
		c, err := Load([]string{args, "logs/"}, env(nil), io.Discard)
		if err != nil || c.SourceGutter != want {
			t.Errorf("%s: got %q, %v; want %q", args, c.SourceGutter, err, want)
		}
	}
	if _, err := Load([]string{"--source-gutter=maybe", "logs/"}, env(nil), io.Discard); err == nil {
		t.Error("an invalid --source-gutter value must be rejected")
	}
	// it still combines with the other positional/flag arguments
	c, err = Load([]string{"--source-gutter", "--dir-mode", "all", "--show-columns", "source", "logs/"}, env(nil), io.Discard)
	if err != nil || c.SourceGutter != "on" || c.Path != "logs/" || c.DirMode != "all" {
		t.Fatalf("bare --source-gutter must not swallow the next argument: %+v, %v", c, err)
	}
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte("directory:\n  mode: all\n  source_gutter: off\n"), 0o644)
	c, err = Load([]string{"--config", f, "logs/"}, env(nil), io.Discard)
	if err != nil || c.SourceGutter != "off" {
		t.Fatalf("directory.source_gutter: off must apply: %+v, %v", c, err)
	}
	c, err = Load([]string{"--config", f, "--source-gutter=on", "logs/"}, env(nil), io.Discard)
	if err != nil || c.SourceGutter != "on" {
		t.Fatalf("the flag must override the config file: %+v, %v", c, err)
	}
	os.WriteFile(f, []byte("directory:\n  source_gutter: sideways\n"), 0o644)
	if _, err := Load([]string{"--config", f, "logs/"}, env(nil), io.Discard); err == nil {
		t.Fatal("an invalid directory.source_gutter in the config file must be reported")
	}
}
