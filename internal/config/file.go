// Applying a parsed config file (see yaml.go) onto a Config.

package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fileSetting maps one dotted config-file key to the code that stores its value.
// set receives the raw string from the file and reports a parse error; the
// caller prefixes the error with the key, so a message always names what to fix.
type fileSetting struct {
	key string
	set func(c *Config, v string) error
}

func intSetting(key string, dst func(*Config) *int) fileSetting {
	return fileSetting{key, func(c *Config, v string) (err error) { *dst(c), err = strconv.Atoi(v); return }}
}

func sizeSetting(key string, dst func(*Config) *int64) fileSetting {
	return fileSetting{key, func(c *Config, v string) (err error) { *dst(c), err = ParseSize(v); return }}
}

func durationSetting(key string, dst func(*Config) *time.Duration) fileSetting {
	return fileSetting{key, func(c *Config, v string) (err error) { *dst(c), err = time.ParseDuration(v); return }}
}

func boolSetting(key string, dst func(*Config) *bool) fileSetting {
	return fileSetting{key, func(c *Config, v string) (err error) { *dst(c), err = strconv.ParseBool(v); return }}
}

func stringSetting(key string, dst func(*Config) *string) fileSetting {
	return fileSetting{key, func(c *Config, v string) error { *dst(c) = v; return nil }}
}

func listSetting(key string, dst func(*Config) *[]string) fileSetting {
	return fileSetting{key, func(c *Config, v string) error { *dst(c) = splitList(v); return nil }}
}

// fileSettings lists every scalar key a config file may set. Adding an option
// is one line here. The order is the order values are applied, and so the order
// in which the first error is found.
var fileSettings = []fileSetting{
	intSetting("buffer.capacity", func(c *Config) *int { return &c.Buffer }),
	sizeSetting("buffer.max_bytes", func(c *Config) *int64 { return &c.BufferBytes }),
	sizeSetting("live.backlog", func(c *Config) *int64 { return &c.Backlog }),
	boolSetting("live.poll", func(c *Config) *bool { return &c.ForcePoll }),
	durationSetting("live.multiline_idle_flush", func(c *Config) *time.Duration { return &c.IdleFlush }),
	durationSetting("performance.batch_interval", func(c *Config) *time.Duration { return &c.BatchInterval }),
	intSetting("performance.max_filter_matches", func(c *Config) *int { return &c.MaxFilterMatches }),
	boolSetting("performance.static_counts", func(c *Config) *bool { return &c.StaticCounts }),
	intSetting("limits.max_lines", func(c *Config) *int { return &c.MaxLines }),
	intSetting("directory.max_files", func(c *Config) *int { return &c.MaxFiles }),
	boolSetting("directory.source_markers", func(c *Config) *bool { return &c.SourceMarkers }),
	{"directory.source_gutter", func(c *Config, v string) error {
		mode, err := ParseGutterMode(v)
		if err == nil { // a bad value leaves the previous mode in place
			c.SourceGutter = mode
		}
		return err
	}},
	stringSetting("directory.mode", func(c *Config) *string { return &c.DirMode }),
	stringSetting("directory.glob", func(c *Config) *string { return &c.Glob }),
	stringSetting("color", func(c *Config) *string { return &c.Color }),
	listSetting("columns.hidden", func(c *Config) *[]string { return &c.HideColumns }),
	listSetting("columns.shown", func(c *Config) *[]string { return &c.ShowColumns }),
	{"limits.max_record_size", func(c *Config, v string) error { // stored as an int, parsed as a size
		n, err := ParseSize(v)
		c.MaxRecordBytes = int(n)
		return err
	}},
	stringSetting("parser", func(c *Config) *string { return &c.Parser }),
	stringSetting("multiline", func(c *Config) *string { return &c.Multiline }),
	stringSetting("search.mode", func(c *Config) *string { return &c.SearchMode }),
}

// fileString returns the string at a dotted path of the parsed file. A missing
// key, or a value that is a section or list rather than a string, is not set.
func fileString(m map[string]any, key string) (string, bool) {
	var cur any = m
	for _, p := range strings.Split(key, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		if cur, ok = mm[p]; !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

// applyMap applies the parsed config file m onto c. It applies everything it
// can and returns the first problem found, so one run reports the first key to fix.
func applyMap(c *Config, m map[string]any) error {
	var firstErr error
	note := func(key string, err error) {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %v", key, err)
		}
	}
	for _, s := range fileSettings {
		if v, ok := fileString(m, s.key); ok {
			note(s.key, s.set(c, v))
		}
	}
	// plugins.<name>.enabled: one boolean per named plugin.
	if pm, ok := m["plugins"].(map[string]any); ok {
		names := make([]string, 0, len(pm))
		for name := range pm {
			names = append(names, name)
		}
		sort.Strings(names) // a fixed order, so the same file always reports the same first error
		for _, name := range names {
			sub, ok := pm[name].(map[string]any)
			if !ok {
				continue
			}
			if e, ok := sub["enabled"].(string); ok {
				b, err := strconv.ParseBool(e)
				note("plugins."+name+".enabled", err)
				c.Plugins[name] = b
			}
		}
	}
	// keys.<action>: the key bindings for one action.
	if km, ok := m["keys"].(map[string]any); ok {
		for action, v := range km {
			if s, ok := v.(string); ok {
				c.Keys[action] = splitList(s)
			}
		}
	}
	return firstErr
}
