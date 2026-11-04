// Friendly diagnostics for malformed command lines (typos, wrong dashes, bad values).

package config

import (
	"errors"
	"flag"
	"fmt"
	"regexp"
	"strings"
)

// splitFlag splits a command-line token into its leading dash count and the
// flag name (before any "=value"). ok is false for tokens that are not
// flag-shaped at all (bare "-", or anything not starting with "-").
func splitFlag(tok string) (dashes int, name string, ok bool) {
	if len(tok) < 2 || tok[0] != '-' {
		return 0, "", false
	}
	rest := tok
	for len(rest) > 0 && rest[0] == '-' {
		dashes++
		rest = rest[1:]
	}
	if dashes > 2 || rest == "" {
		return dashes, "", false // "--" itself, or "---x": not our concern, let flag.Parse react
	}
	if i := strings.IndexByte(rest, '='); i >= 0 {
		rest = rest[:i]
	}
	return dashes, rest, true
}

// editDistance is the Levenshtein distance between a and b, used only to
// suggest the flag the person most likely meant.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del, ins, sub := prev[j]+1, cur[j-1]+1, prev[j-1]+cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// closestFlagName returns the registered flag name closest to bad, if any is
// close enough to plausibly be what was meant (allowing roughly one edit per
// three characters, at least 1).
func closestFlagName(bad string, fs *flag.FlagSet) string {
	best, bestDist := "", 1<<30
	fs.VisitAll(func(f *flag.Flag) {
		if d := editDistance(bad, f.Name); d < bestDist {
			best, bestDist = f.Name, d
		}
	})
	if best == "" || bestDist > 1+len(bad)/3 {
		return ""
	}
	return best
}

// validateDashes enforces GNU-style dash conventions before Go's own flag
// parser ever runs: a multi-character flag name needs "--" ("--dir-mode"),
// never a single dash ("-dir-mode") — which flag.Parse would otherwise accept
// silently, since it treats "-x" and "--x" as identical regardless of length.
// A single-character flag ("-f") is unaffected. A bare "--" ends the scan, the
// same point at which flag.Parse itself stops treating things as flags.
func validateDashes(args []string, fs *flag.FlagSet) error {
	for _, a := range args {
		if a == "--" {
			return nil
		}
		dashes, name, ok := splitFlag(a)
		if !ok || dashes != 1 || len(name) <= 1 {
			continue
		}
		msg := fmt.Sprintf("unrecognized flag -%s: multi-character flags need two dashes", name)
		known := false
		fs.VisitAll(func(f *flag.Flag) { known = known || f.Name == name })
		switch {
		case known:
			msg = fmt.Sprintf("unrecognized flag -%s: did you mean --%s?", name, name)
		default:
			if s := closestFlagName(name, fs); s != "" {
				msg = fmt.Sprintf("unrecognized flag -%s: multi-character flags need two dashes; did you mean --%s?", name, s)
			}
		}
		return errors.New(msg)
	}
	return nil
}

// explainParseError turns Go's own "flag provided but not defined: -x" (always
// rendered with a single dash, however many the person typed) into a message
// that also suggests the closest known flag, e.g. for a typo like
// "--dir-mod" instead of "--dir-mode". One-letter flags get no suggestion: with
// so few characters, "closest" would be meaningless (-v is not a typo of -f).
func explainParseError(err error, fs *flag.FlagSet) error {
	const prefix = "flag provided but not defined: -"
	msg := err.Error()
	if m := needsArgRE.FindStringSubmatch(msg); m != nil {
		return fmt.Errorf("%s needs a value (see logi --help)", dashed(m[1]))
	}
	if m := invalidValueRE.FindStringSubmatch(msg); m != nil {
		why := strings.TrimSpace(m[3])
		switch why {
		case "parse error":
			why = "not a valid number"
		case "value out of range":
			why = "number out of range"
		}
		return fmt.Errorf("invalid value %q for %s: %s", m[1], dashed(m[2]), why)
	}
	if !strings.HasPrefix(msg, prefix) {
		return err
	}
	bad := strings.TrimPrefix(msg, prefix)
	if len(bad) == 1 {
		return fmt.Errorf("unrecognized flag -%s (see logi --help)", bad)
	}
	if s := closestFlagName(bad, fs); s != "" {
		dashes := "--"
		if len(s) == 1 {
			dashes = "-"
		}
		return fmt.Errorf("unrecognized flag --%s: did you mean %s%s?", bad, dashes, s)
	}
	return fmt.Errorf("unrecognized flag --%s (see logi --help)", bad)
}

var (
	invalidValueRE = regexp.MustCompile(`^invalid (?:boolean )?value "(.*)" for (?:flag )?-([^\s:]+): (.*)$`)
	needsArgRE     = regexp.MustCompile(`^flag needs an argument: -(\S+)$`)
)
