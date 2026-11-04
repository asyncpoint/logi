// Validation of a resolved Config and the helpers that word its errors.

package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// validColumn checks the shape of a column name. Names beyond the built-in ones
// (id, time, level, source, message) come from the log format in use, e.g.
// "thread" or "logger" for NCache, so they cannot be enumerated here.
func validColumn(n string) bool {
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		return false
	}
	for _, c := range n {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// enumErr reports a value that is not one of options, suggesting the closest.
func enumErr(what, got string, options ...string) error {
	list := options[0]
	if len(options) > 1 {
		list = strings.Join(options[:len(options)-1], ", ") + " or " + options[len(options)-1]
	}
	msg := fmt.Sprintf("%s must be %s, got %q", what, list, got)
	if s := closestOf(got, options); s != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", s)
	}
	return errors.New(msg)
}

// closestOf returns the option nearest to bad when it is plausibly a typo.
func closestOf(bad string, options []string) string {
	best, bestDist := "", 1<<30
	for _, o := range options {
		if d := editDistance(strings.ToLower(bad), o); d < bestDist {
			best, bestDist = o, d
		}
	}
	if best == "" || bestDist > 1+len(bad)/3 {
		return ""
	}
	return best
}

func oneOf(v string, options ...string) bool {
	for _, o := range options {
		if v == o {
			return true
		}
	}
	return false
}

// Validate checks value ranges. Fixed-choice settings are matched without regard
// to case ("--dir-mode ALL" works) and normalised to lower case.
func (c *Config) Validate() error {
	for _, v := range []*string{&c.SearchMode, &c.Color, &c.DirMode, &c.Multiline, &c.SourceGutter, &c.Parser} {
		*v = strings.ToLower(strings.TrimSpace(*v))
	}
	switch {
	case c.Buffer < 1:
		return errors.New("buffer must be at least 1 record")
	case c.MaxRecordBytes < 64:
		return errors.New("max record size must be at least 64 bytes")
	case c.MaxLines < 1:
		return errors.New("max lines must be at least 1")
	case c.MaxFilterMatches < 1:
		return errors.New("max filter matches must be at least 1")
	case !oneOf(c.Multiline, "auto", "off"):
		return enumErr("multiline", c.Multiline, "auto", "off")
	case !oneOf(c.SearchMode, "literal", "regex", "field"):
		return enumErr("search mode", c.SearchMode, "literal", "regex", "field")
	case c.BatchInterval < time.Millisecond:
		return errors.New("batch interval must be at least 1ms")
	case !oneOf(c.SourceGutter, "auto", "on", "off"):
		return enumErr("source gutter", c.SourceGutter, "auto", "on", "off")
	case !oneOf(c.Color, "auto", "always", "never"):
		return enumErr("color", c.Color, "auto", "always", "never")
	case !oneOf(c.DirMode, "latest", "all"):
		return enumErr("directory mode", c.DirMode, "latest", "all")
	case c.MaxFiles < 1:
		return errors.New("max files must be at least 1")
	case c.Lines < 0:
		return errors.New("lines must be 0 or more")
	}
	for _, list := range [][]string{c.HideColumns, c.ShowColumns} {
		for _, n := range list {
			if !validColumn(n) {
				return fmt.Errorf("invalid column name %q (use lower-case names such as id, time, level, source, message, thread)", n)
			}
		}
	}
	return nil
}
