package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// helpItem is one line of --help. names lists the registered flags it documents
// (empty for positional arguments), so a test can prove nothing is left out.
type helpItem struct {
	usage string
	desc  string
	names []string
}

type helpGroup struct {
	title string
	items []helpItem
}

// humanSize renders a byte count the way it can be typed back: 8 MiB, 64 KiB.
func humanSize(n int64) string {
	const k = 1024
	switch {
	case n <= 0:
		return "0"
	case n%(k*k*k) == 0:
		return fmt.Sprintf("%d GiB", n/(k*k*k))
	case n%(k*k) == 0:
		return fmt.Sprintf("%d MiB", n/(k*k))
	case n%k == 0:
		return fmt.Sprintf("%d KiB", n/k)
	case n >= k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}

// helpGroups describes every option, grouped by what it is for. Defaults are
// read from c, so help shows the values in effect (config file and environment
// included), in readable units.
func helpGroups(c *Config) []helpGroup {
	return []helpGroup{
		{"Input", []helpItem{
			{"<file>", "a log file: opens in the browser (add -f to follow it as it grows)", nil},
			{"<directory>", "pick a file from it; with -f follow the newest file (see --dir-mode)", nil},
			{"<file> <file> ...", "several files in one directory: followed together in one merged view. Wildcards such as logs\\*.log work on Windows too", nil},
			{"-", "read standard input; this happens automatically when input is piped, e.g. kubectl logs web | logi", nil},
		}},
		{"Live view", []helpItem{
			{"-f, --follow", "follow the file as it grows, like tail -f (alias: --live)", []string{"follow", "live", "f"}},
			{"-n, --lines <n>", "follow, starting with only the last n lines of the file", []string{"lines", "n"}},
			{"--from-start", "follow, but read the whole file first", []string{"from-start"}},
			{"--backlog <size>", fmt.Sprintf("history to load before following, e.g. 512KiB, 8MiB; 0 = only new data (default %s)", humanSize(c.Backlog)), []string{"backlog"}},
			{"--buffer <records>", fmt.Sprintf("how many records to keep in memory (default %d)", c.Buffer), []string{"buffer"}},
			{"--buffer-bytes <size>", fmt.Sprintf("memory budget for kept records; 0 = unlimited (default %s)", humanSize(c.BufferBytes)), []string{"buffer-bytes"}},
			{"--poll", "check the file on a timer instead of using file-system notifications (network drives)", []string{"poll"}},
		}},
		{"Directories", []helpItem{
			{"--dir-mode <latest|all>", fmt.Sprintf("latest: follow the newest file and roll over to new ones; all: follow every matching file at once (default %s)", c.DirMode), []string{"dir-mode"}},
			{"--glob <patterns>", "which files count, comma separated, e.g. '*.log,*.txt' (default: all log-like files)", []string{"glob"}},
			{"--max-files <n>", fmt.Sprintf("most files followed at once in 'all' mode (default %d)", c.MaxFiles), []string{"max-files"}},
			{"--source-gutter[=auto|on|off]", "file-colour bar in the row gutter, 'all' mode; auto shows it while the source column is hidden (default auto)", []string{"source-gutter"}},
			{"--source-markers", "with more than ten files, mark repeated colours with a shape (◆ ▲ ■ ●)", []string{"source-markers"}},
		}},
		{"Filtering at start", []helpItem{
			{"--filter <query>", "full filter query, terms ANDed, e.g. 'severity:>=warn message:timeout since:15m'", []string{"filter"}},
			{"--level <names>", "only these severities, e.g. warn,error (repeatable)", []string{"level"}},
			{"--contains <text>", "only records containing this text, ignoring case (repeatable)", []string{"contains"}},
			{"--regex <pattern>", "only records matching this regular expression (repeatable)", []string{"regex"}},
			{"--field <key=value>", "only records whose field equals value, e.g. host=server01 (repeatable)", []string{"field"}},
			{"--exclude <text>", "drop records containing this text (repeatable)", []string{"exclude"}},
			{"--since <time>", "only records from this time on: 15m, 2h, 1d (back from the newest record), 14:30, or 2026-10-15 14:30", []string{"since"}},
			{"--until <time>", "only records up to this time (same formats; a bare date includes that whole day)", []string{"until"}},
			{"--search-mode <mode>", fmt.Sprintf("how the in-app search reads your text: literal, regex or field (default %s)", c.SearchMode), []string{"search-mode"}},
		}},
		{"Display", []helpItem{
			{"--color <auto|always|never>", fmt.Sprintf("colours (default %s; NO_COLOR is honoured)", c.Color), []string{"color"}},
			{"--hide-columns <names>", "columns to hide at start, e.g. id,source (names depend on the log format)", []string{"hide-columns"}},
			{"--show-columns <names>", "columns to show at start, e.g. source", []string{"show-columns"}},
		}},
		{"Parsing and limits", []helpItem{
			{"--parser <name>", fmt.Sprintf("log format: auto, generic, json, ncache, ncache-events; see --list-parsers (default %s)", c.Parser), []string{"parser"}},
			{"--no-multiline", "treat every line as its own record (no stack-trace joining)", []string{"no-multiline"}},
			{"--max-record-size <size>", fmt.Sprintf("cut records longer than this (default %s)", humanSize(int64(c.MaxRecordBytes))), []string{"max-record-size"}},
			{"--max-lines <n>", fmt.Sprintf("cut multi-line records after this many lines (default %d)", c.MaxLines), []string{"max-lines"}},
			{"--enable-plugin <name>", "turn a built-in plugin on (repeatable)", []string{"enable-plugin"}},
			{"--disable-plugin <name>", "turn a built-in plugin off, e.g. ncache (repeatable)", []string{"disable-plugin"}},
		}},
		{"Other", []helpItem{
			{"--config <path>", "read settings from this file", []string{"config"}},
			{"--list-parsers", "list the log formats logi understands, then exit", []string{"list-parsers"}},
			{"-V, --version", "print the version, then exit", []string{"version", "V"}},
			{"-h, --help", "show this help", nil},
		}},
	}
}

const (
	helpUsageWidth = 31
	helpWidth      = 100
)

// wrap breaks text into lines of at most width runes at spaces.
func wrap(text string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		if cur != "" && len([]rune(cur))+1+len([]rune(w)) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur != "" {
			cur += " "
		}
		cur += w
	}
	return append(lines, cur)
}

func printItem(w io.Writer, it helpItem) {
	lines := wrap(it.desc, helpWidth-helpUsageWidth-3)
	pad := strings.Repeat(" ", helpUsageWidth+3) // two leading spaces, the usage column, one gap
	usage := it.usage
	if len([]rune(usage)) >= helpUsageWidth {
		fmt.Fprintf(w, "  %s\n", usage)
		for _, l := range lines {
			fmt.Fprintf(w, "%s%s\n", pad, l)
		}
		return
	}
	fmt.Fprintf(w, "  %-*s %s\n", helpUsageWidth, usage, lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(w, "%s%s\n", pad, l)
	}
}

func configFilePath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "logi", "config.yaml")
	}
	return "config.yaml in your user config directory"
}

// printHelp writes the grouped --help text.
func printHelp(w io.Writer, c *Config) {
	fmt.Fprint(w, `logi — a lightweight, terminal-first log inspector

Usage:
  logi [options] <file>              browse a file (add -f to follow it)
  logi [options] <directory>         pick a file, or follow a directory with -f
  logi [options] <file> <file> ...   follow several files in one directory, merged
  <command> | logi [options]         read standard input
`)
	for _, g := range helpGroups(c) {
		fmt.Fprintf(w, "\n%s:\n", g.title)
		for _, it := range g.items {
			printItem(w, it)
		}
	}
	fmt.Fprintf(w, `
Settings are read from the config file, then environment variables, then
command-line flags; later ones win.
  Config file   %s
  Environment   LOGI_CONFIG (file), LOGI_FOLLOW, LOGI_PARSER, LOGI_COLOR,
                LOGI_BUFFER, LOGI_DISABLE_PLUGINS; NO_COLOR turns colours off
  Boolean flags take =false to switch off, e.g. --follow=false

Examples:
  Browse a large file without loading it all into memory:
    logi application.log

  Follow a file, starting from its last 500 lines:
    logi -n 500 application.log

  Show only the last 15 minutes of warnings and errors:
    logi --since 15m --level warn,error application.log

  Follow several files at once, merged and colour-coded:
    logi -f logs/api.log logs/web.log logs/worker.log
    logi -f --dir-mode all --glob '*.log' logs/

  Follow the newest file across daily rollovers:
    logi -f /var/log/ncache/

  Docker and Kubernetes (logi reads the pipe; each service or pod gets its own colour):
    docker logs -f web 2>&1 | logi              2>&1: docker prints the container's stderr on stderr
    docker compose logs -f | logi
    kubectl logs -f deploy/web | logi
    kubectl logs -f -l app=web --prefix --all-containers | logi

  Follow matching records, filtered at start:
    logi -f app.log --level warn,error --contains "connection refused"

  Force a parser, or hide columns at start:
    logi --parser ncache application.log
    logi --hide-columns id,source app.log
`, configFilePath())
}
