package config

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/asyncpoint/logi/internal/filter"
)

// The user documentation is part of the product, so it is tested: every command
// it shows must parse, every option must be documented, and the text must stay
// about logi rather than about its author.

var docFiles = []string{"../../README.md", "../../docs/HOWTO.md"}

func readDoc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("%s not available: %v", path, err)
	}
	return string(b)
}

// fencedLines returns the lines inside ``` fences, each with the marker comment
// that directly preceded its fence, if any.
type fencedLine struct {
	text   string
	marker string
	line   int
}

func fencedLines(doc string) []fencedLine {
	var out []fencedLine
	in := false
	marker, prev := "", ""
	n := 0
	sc := bufio.NewScanner(strings.NewReader(doc))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		n++
		l := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			in = !in
			if in {
				marker = ""
				if strings.HasPrefix(prev, "<!--") {
					marker = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(prev, "<!--"), "-->"))
				}
			}
			continue
		}
		if in {
			out = append(out, fencedLine{l, marker, n})
		} else if strings.TrimSpace(l) != "" {
			prev = strings.TrimSpace(l)
		}
	}
	return out
}

// shellSplit splits a command line the way a shell would for the simple cases
// the documents use: single and double quotes, no escapes, '#' starts a comment.
func shellSplit(s string) []string {
	var toks []string
	var cur strings.Builder
	has := false
	var q rune
	for _, r := range s {
		switch {
		case q != 0:
			if r == q {
				q = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			q, has = r, true
		case r == '#' && !has && cur.Len() == 0:
			return toks
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}

var envAssign = regexp.MustCompile(`^[A-Z_]+=`)

// logiArgs extracts the arguments of a logi command from a documentation line,
// or reports false when the line is not a runnable logi command.
func logiArgs(line string) ([]string, bool) {
	l := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
	if l == "" || strings.HasPrefix(l, "cmd /c") || strings.ContainsAny(l, "<…") {
		return nil, false
	}
	if i := strings.LastIndex(l, "| logi"); i >= 0 {
		l = l[i+2:]
	}
	toks := shellSplit(l)
	for len(toks) > 0 && envAssign.MatchString(toks[0]) {
		toks = toks[1:]
	}
	if len(toks) == 0 || toks[0] != "logi" {
		return nil, false
	}
	return toks[1:], true
}

func TestDocsCommandExamplesParse(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("HOME", cfgHome)
	t.Setenv("AppData", cfgHome)
	total := 0
	for _, path := range docFiles {
		for _, fl := range fencedLines(readDoc(t, path)) {
			args, ok := logiArgs(fl.text)
			if !ok {
				continue
			}
			total++
			cfg, err := Load(args, func(string) string { return "" }, io.Discard)
			if err != nil {
				msg := err.Error()
				switch {
				case errors.Is(err, flag.ErrHelp):
				case strings.Contains(msg, "no files match"), // wildcard examples name files that do not exist here
					strings.Contains(msg, "no such file"), strings.Contains(msg, "cannot find"),
					strings.Contains(msg, "does not exist"), strings.Contains(msg, "not found"):
				default:
					t.Errorf("%s:%d: %q does not parse: %v", path, fl.line, strings.TrimSpace(fl.text), err)
				}
				continue
			}
			if cfg.Filter != "" {
				if _, err := filter.Compile(cfg.Filter); err != nil {
					t.Errorf("%s:%d: filter of %q does not compile: %v", path, fl.line, strings.TrimSpace(fl.text), err)
				}
			}
		}
	}
	if total < 60 {
		t.Fatalf("only %d command examples found in the documents; the extraction is probably broken", total)
	}
}

func TestDocsFilterExamplesCompile(t *testing.T) {
	n := 0
	for _, path := range docFiles {
		for _, fl := range fencedLines(readDoc(t, path)) {
			if fl.marker != "filter-examples" || strings.TrimSpace(fl.text) == "" {
				continue
			}
			n++
			if _, err := filter.Compile(strings.TrimSpace(fl.text)); err != nil {
				t.Errorf("%s:%d: filter %q does not compile: %v", path, fl.line, strings.TrimSpace(fl.text), err)
			}
		}
	}
	if n < 8 {
		t.Fatalf("only %d marked filter examples found", n)
	}
}

// Every command-line option appears in the README options table, and every
// option except plain aliases is also covered by the how-to guide.
func TestEveryOptionIsDocumented(t *testing.T) {
	readme := readDoc(t, "../../README.md")
	howto := readDoc(t, "../../docs/HOWTO.md")
	c := Defaults()
	var v flagLocals
	for _, d := range flagDefs(&c, &v) {
		for i, n := range d.names {
			dash := "--"
			if len(n) == 1 {
				dash = "-"
			}
			if !strings.Contains(readme, "`"+dash+n) && !strings.Contains(readme, ", `"+dash+n) {
				t.Errorf("README.md does not document %s%s", dash, n)
			}
			if i == 0 && !strings.Contains(howto, dash+n) {
				t.Errorf("docs/HOWTO.md never mentions %s%s", dash, n)
			}
		}
	}
}

var firstPerson = regexp.MustCompile(`(^|[^A-Za-z0-9_])(I|I'm|I'd|I've|I'll|my|me|mine|myself)([^A-Za-z0-9_']|$)`)

// Documents are written about logi, not in the voice of its author.
func TestDocsAreNotWrittenInTheFirstPerson(t *testing.T) {
	files := []string{"../../README.md", "../../CONTRIBUTING.md", "../../CHANGELOG.md", "../../RELEASE-NOTES.md", "../../BENCHMARKS.md",
		"../../docs/HOWTO.md", "../../docs/TDD.md", "../../docs/STABILITY.md", "../../docs/implementation-baseline.md",
		"../../docs/performance-baseline.md", "../../docs/ADR/ADR.md"}
	inlineCode := regexp.MustCompile("`[^`]*`")
	for _, path := range files {
		doc := readDoc(t, path)
		fenced := map[int]bool{}
		for _, fl := range fencedLines(doc) {
			fenced[fl.line] = true
		}
		for i, l := range strings.Split(doc, "\n") {
			if fenced[i+1] {
				continue
			}
			text := inlineCode.ReplaceAllString(l, " ")
			if firstPerson.MatchString(text) {
				t.Errorf("%s:%d is written in the first person: %s", path, i+1, strings.TrimSpace(l))
			}
		}
	}
}

var mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// slug builds the anchor GitHub generates for a heading.
func slug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func anchors(doc string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "#") {
			out[slug(strings.TrimLeft(l, "# "))] = true
		}
	}
	return out
}

// Every relative link in the documents points at a file that exists and, where
// it names a heading, at a heading that exists.
func TestDocsLinksResolve(t *testing.T) {
	for _, path := range docFiles {
		doc := readDoc(t, path)
		dir := path[:strings.LastIndex(path, "/")+1]
		for _, m := range mdLink.FindAllStringSubmatch(doc, -1) {
			target := m[1]
			if strings.Contains(target, "://") {
				continue
			}
			file, frag, _ := strings.Cut(target, "#")
			targetDoc := doc
			if file != "" {
				b, err := os.ReadFile(dir + file)
				if err != nil {
					if _, serr := os.Stat(dir + file); serr != nil {
						t.Errorf("%s links to %q, which does not exist", path, target)
					}
					continue
				}
				targetDoc = string(b)
			}
			if frag != "" && strings.HasSuffix(strings.TrimSuffix(file, "/"), ".md") || file == "" && frag != "" {
				if !anchors(targetDoc)[frag] {
					t.Errorf("%s links to %q, but that heading does not exist", path, target)
				}
			}
		}
	}
}
