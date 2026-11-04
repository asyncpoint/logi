package config

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// applyMapRef is the original hand-written implementation, kept as the
// specification the table-driven applyMap must agree with.
func applyMapRef(c *Config, m map[string]any) error {
	get := func(path ...string) (string, bool) {
		var cur any = m
		for _, p := range path {
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
	var firstErr error
	setInt := func(dst *int, path ...string) {
		if v, ok := get(path...); ok {
			n, err := strconv.Atoi(v)
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %v", strings.Join(path, "."), err)
			}
			*dst = n
		}
	}
	setSize := func(dst *int64, path ...string) {
		if v, ok := get(path...); ok {
			n, err := ParseSize(v)
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %v", strings.Join(path, "."), err)
			}
			*dst = n
		}
	}
	setDur := func(dst *time.Duration, path ...string) {
		if v, ok := get(path...); ok {
			d, err := time.ParseDuration(v)
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %v", strings.Join(path, "."), err)
			}
			*dst = d
		}
	}
	setBool := func(dst *bool, path ...string) {
		if v, ok := get(path...); ok {
			b, err := strconv.ParseBool(v)
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %v", strings.Join(path, "."), err)
			}
			*dst = b
		}
	}
	setInt(&c.Buffer, "buffer", "capacity")
	setSize(&c.BufferBytes, "buffer", "max_bytes")
	setSize(&c.Backlog, "live", "backlog")
	setBool(&c.ForcePoll, "live", "poll")
	setDur(&c.IdleFlush, "live", "multiline_idle_flush")
	setDur(&c.BatchInterval, "performance", "batch_interval")
	setInt(&c.MaxFilterMatches, "performance", "max_filter_matches")
	setInt(&c.MaxLines, "limits", "max_lines")
	setInt(&c.MaxFiles, "directory", "max_files")
	setBool(&c.SourceMarkers, "directory", "source_markers")
	if v, ok := get("directory", "source_gutter"); ok {
		mode, err := ParseGutterMode(v)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("directory.source_gutter: %v", err)
		}
		if err == nil {
			c.SourceGutter = mode
		}
	}
	if v, ok := get("directory", "mode"); ok {
		c.DirMode = v
	}
	if v, ok := get("directory", "glob"); ok {
		c.Glob = v
	}
	if v, ok := get("color"); ok {
		c.Color = v
	}
	if v, ok := get("columns", "hidden"); ok {
		c.HideColumns = splitList(v)
	}
	if v, ok := get("columns", "shown"); ok {
		c.ShowColumns = splitList(v)
	}
	var mr int64 = int64(c.MaxRecordBytes)
	setSize(&mr, "limits", "max_record_size")
	c.MaxRecordBytes = int(mr)
	if v, ok := get("parser"); ok {
		c.Parser = v
	}
	if v, ok := get("multiline"); ok {
		c.Multiline = v
	}
	if v, ok := get("search", "mode"); ok {
		c.SearchMode = v
	}
	if pm, ok := m["plugins"].(map[string]any); ok {
		names := make([]string, 0, len(pm))
		for name := range pm {
			names = append(names, name)
		}
		sort.Strings(names) // the one deliberate change from the original: a fixed order
		for _, name := range names {
			if sub, ok := pm[name].(map[string]any); ok {
				if e, ok := sub["enabled"].(string); ok {
					b, err := strconv.ParseBool(e)
					if err != nil && firstErr == nil {
						firstErr = fmt.Errorf("plugins.%s.enabled: %v", name, err)
					}
					c.Plugins[name] = b
				}
			}
		}
	}
	if km, ok := m["keys"].(map[string]any); ok {
		for action, v := range km {
			if s, ok := v.(string); ok {
				c.Keys[action] = splitList(s)
			}
		}
	}
	return firstErr
}

// randomFileMap builds a config-file map from the known keys with a mix of valid
// values, invalid values, non-string values and unknown keys.
func randomFileMap(rng *rand.Rand) map[string]any {
	vals := map[string][]string{
		"int":    {"0", "1", "500", "-3", "abc", "", "1.5", "99999999999999999999"},
		"size":   {"0", "64KiB", "1MB", "2GiB", "10", "-1", "huge", "1.5MiB", ""},
		"dur":    {"0s", "50ms", "2s", "1m", "abc", "", "10"},
		"bool":   {"true", "false", "1", "0", "T", "yes", "", "maybe"},
		"gutter": {"auto", "on", "off", "wide", "", "true"},
		"str":    {"a", "generic", "regex", "newest", "*.log", "", "never", "x y"},
		"list":   {"time,level", "a", "", " a , b ,, c ", "message"},
	}
	type k struct {
		path []string
		kind string
	}
	keys := []k{
		{[]string{"buffer", "capacity"}, "int"}, {[]string{"buffer", "max_bytes"}, "size"},
		{[]string{"live", "backlog"}, "size"}, {[]string{"live", "poll"}, "bool"},
		{[]string{"live", "multiline_idle_flush"}, "dur"}, {[]string{"performance", "batch_interval"}, "dur"},
		{[]string{"performance", "max_filter_matches"}, "int"}, {[]string{"limits", "max_lines"}, "int"},
		{[]string{"limits", "max_record_size"}, "size"}, {[]string{"directory", "max_files"}, "int"},
		{[]string{"directory", "source_markers"}, "bool"}, {[]string{"directory", "source_gutter"}, "gutter"},
		{[]string{"directory", "mode"}, "str"}, {[]string{"directory", "glob"}, "str"},
		{[]string{"color"}, "str"}, {[]string{"columns", "hidden"}, "list"}, {[]string{"columns", "shown"}, "list"},
		{[]string{"parser"}, "str"}, {[]string{"multiline"}, "str"}, {[]string{"search", "mode"}, "str"},
	}
	m := map[string]any{}
	set := func(path []string, v any) {
		cur := m
		for _, p := range path[:len(path)-1] {
			next, ok := cur[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[p] = next
			}
			cur = next
		}
		cur[path[len(path)-1]] = v
	}
	for _, kk := range keys {
		switch rng.Intn(5) {
		case 0, 1: // absent
		case 2: // wrong shape: a map or list where a string belongs
			if rng.Intn(2) == 0 {
				set(kk.path, map[string]any{"x": "1"})
			} else {
				set(kk.path, []any{"1"})
			}
		default:
			set(kk.path, vals[kk.kind][rng.Intn(len(vals[kk.kind]))])
		}
	}
	if rng.Intn(2) == 0 {
		pl := map[string]any{}
		for _, n := range []string{"ncache", "other"} {
			switch rng.Intn(4) {
			case 0:
				pl[n] = map[string]any{"enabled": vals["bool"][rng.Intn(len(vals["bool"]))]}
			case 1:
				pl[n] = "not-a-map"
			case 2:
				pl[n] = map[string]any{"enabled": []any{"x"}}
			}
		}
		m["plugins"] = pl
	}
	if rng.Intn(2) == 0 {
		km := map[string]any{}
		for _, a := range []string{"copy", "quit", "filter"} {
			switch rng.Intn(3) {
			case 0:
				km[a] = vals["list"][rng.Intn(len(vals["list"]))]
			case 1:
				km[a] = []any{"y"}
			}
		}
		m["keys"] = km
	}
	if rng.Intn(3) == 0 {
		m["unknown_key"] = "1"
		set([]string{"buffer", "unknown"}, "1")
	}
	return m
}

func TestApplyMapMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	n := 20000
	if testing.Short() {
		n = 2000
	}
	for i := 0; i < n; i++ {
		m := randomFileMap(rng)
		a, b := Defaults(), Defaults()
		errA, errB := applyMap(&a, m), applyMapRef(&b, m)
		if fmt.Sprint(errA) != fmt.Sprint(errB) {
			t.Fatalf("case %d: error %v, reference %v\nmap: %v", i, errA, errB, m)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("case %d: config differs\n got %+v\nwant %+v\nmap: %v", i, a, b, m)
		}
	}
}

func TestFileSettingKeysAreUniqueAndWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range fileSettings {
		if s.key == "" || strings.HasPrefix(s.key, ".") || strings.HasSuffix(s.key, ".") || strings.Contains(s.key, "..") {
			t.Errorf("malformed key %q", s.key)
		}
		if seen[s.key] {
			t.Errorf("duplicate key %q", s.key)
		}
		seen[s.key] = true
		if s.set == nil {
			t.Errorf("key %q has no setter", s.key)
		}
	}
}

func TestApplyMapNamesTheKeyInErrors(t *testing.T) {
	c := Defaults()
	err := applyMap(&c, map[string]any{"live": map[string]any{"poll": "maybe"}, "buffer": map[string]any{"capacity": "x"}})
	if err == nil || !strings.HasPrefix(err.Error(), "buffer.capacity: ") {
		t.Fatalf("want the first key in table order named, got %v", err)
	}
	c = Defaults()
	err = applyMap(&c, map[string]any{"plugins": map[string]any{"b": map[string]any{"enabled": "?"}, "a": map[string]any{"enabled": "?"}}})
	if err == nil || !strings.HasPrefix(err.Error(), "plugins.a.enabled: ") {
		t.Fatalf("plugin errors must be reported in name order, got %v", err)
	}
}

// docs/config.example.yaml is what users copy from; every key the loader reads
// must appear in it, and it must stay loadable.
func TestExampleConfigCoversEveryKey(t *testing.T) {
	src, err := os.ReadFile("../../docs/config.example.yaml")
	if err != nil {
		t.Skip("example config not available:", err)
	}
	m, err := parseYAML(string(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range fileSettings {
		if _, ok := fileString(m, s.key); !ok {
			t.Errorf("docs/config.example.yaml does not document %q", s.key)
		}
	}
	c := Defaults()
	if err := applyMap(&c, m); err != nil {
		t.Fatalf("the example config must load cleanly: %v", err)
	}
}
