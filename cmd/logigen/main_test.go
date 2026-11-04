package main

import (
	"bytes"
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"
)

func generate(format string, n int, numbered bool) string {
	var b bytes.Buffer
	r := rand.New(rand.NewSource(1))
	base := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		record(&b, format, i, r, base.Add(time.Duration(i)*50*time.Millisecond), 1, numbered)
	}
	return b.String()
}

// Generated messages look like real ones: no "(item N)" suffix unless asked for.
func TestRecordsHaveNoItemSuffixByDefault(t *testing.T) {
	for _, format := range []string{"text", "ncache", "json"} {
		if out := generate(format, 200, false); strings.Contains(out, "(item") {
			t.Errorf("%s: default output contains an item suffix", format)
		}
		if format != "json" {
			if out := generate(format, 200, true); !strings.Contains(out, "(item 199") {
				t.Errorf("%s: -numbered output has no item suffix", format)
			}
		}
	}
}

// -numbered only adds the suffix: for a given seed everything else is identical,
// so numbered and plain files describe the same records.
func TestNumberedOnlyAddsTheSuffix(t *testing.T) {
	strip := map[string]func(string) string{
		"text": func(s string) string {
			return regexp.MustCompile(`\(item \d+, (\d+ms)\)`).ReplaceAllString(s, "($1)")
		},
		"ncache": func(s string) string {
			return regexp.MustCompile(` \(item \d+\)\n`).ReplaceAllString(s, "\n")
		},
	}
	for format, fn := range strip {
		if got, want := fn(generate(format, 300, true)), generate(format, 300, false); got != want {
			t.Errorf("%s: plain and numbered output differ beyond the suffix", format)
		}
	}
}
