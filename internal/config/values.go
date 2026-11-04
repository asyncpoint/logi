// Parsers for individual setting values shared by flags, environment and file.

package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize parses "64k", "8MiB", "1G", "1048576" into bytes.
func ParseSize(s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30}, {"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"b", 1}} {
		if strings.HasSuffix(t, u.suf) {
			mult, t = u.m, strings.TrimSuffix(t, u.suf)
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n * mult, nil
}

func splitList(s string) (out []string) {
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return
}

// ParseGutterMode normalises a source-gutter setting: auto, on or off (true and
// false are accepted as on and off).
func ParseGutterMode(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "auto":
		return "auto", nil
	case "on", "true", "yes", "1":
		return "on", nil
	case "off", "false", "no", "0":
		return "off", nil
	}
	return "", fmt.Errorf("must be auto, on or off, got %q", v)
}
