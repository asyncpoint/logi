// Turning positional arguments into the files and directories to open.

package config

import (
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func hasGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// resolvePaths turns the positional arguments into the session's input:
//
//	logi -                 standard input
//	logi file | dir        as given
//	logi a.log b.log ...   several files in one directory: followed together
//
// Wildcards are expanded here when the shell did not (Windows shells do not).
func resolvePaths(c *Config, pos []string, explicit map[string]bool) error {
	if len(pos) == 0 {
		return nil
	}
	for _, p := range pos {
		if p == "-" && len(pos) > 1 {
			return errors.New(`"-" (standard input) cannot be combined with files`)
		}
	}
	if len(pos) == 1 && pos[0] == "-" {
		c.Stdin, c.Follow, c.Path = true, true, ""
		return nil
	}
	var paths []string
	for _, p := range pos {
		if hasGlobMeta(p) {
			if _, err := os.Stat(p); err != nil { // not a literal file name: expand it ourselves
				m, gerr := filepath.Glob(p)
				if gerr != nil {
					return fmt.Errorf("bad pattern %q: %v", p, gerr)
				}
				if len(m) == 0 {
					return fmt.Errorf("no files match %q", p)
				}
				sort.Strings(m)
				paths = append(paths, m...)
				continue
			}
		}
		paths = append(paths, p)
	}
	if len(paths) == 1 {
		c.Path = paths[0]
		return nil
	}
	return followFiles(c, paths, explicit)
}

// followFiles sets up a merged live view of several files. They must share a
// directory; the directory follower already merges a chosen set of names.
func followFiles(c *Config, paths []string, explicit map[string]bool) error {
	if explicit["glob"] {
		return errors.New("--glob cannot be combined with several files: the files you list are the selection")
	}
	var dir, dirAbs string
	seen := map[string]bool{}
	var pats []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return PathError(p, err)
		}
		if fi.IsDir() {
			return fmt.Errorf("%q is a directory: give several files, or a single directory", p)
		}
		d, name := filepath.Split(filepath.Clean(p))
		d = filepath.Clean(d)
		abs, _ := filepath.Abs(d)
		if dir == "" {
			dir, dirAbs = d, abs
		} else if abs != dirAbs {
			return fmt.Errorf("several files must be in the same directory (got %s and %s); to watch more places, run a second logi", dir, d)
		}
		if strings.Contains(name, ",") {
			return fmt.Errorf("%q: file names containing a comma are not supported when giving several files", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		var esc strings.Builder
		for _, r := range name { // match the name literally, wildcards included
			if r == '*' || r == '?' || r == '[' {
				esc.WriteString("[" + string(r) + "]")
			} else {
				esc.WriteRune(r)
			}
		}
		pats = append(pats, esc.String())
	}
	c.Path, c.DirMode, c.Follow, c.Glob = dir, "all", true, strings.Join(pats, ",")
	if len(pats) > c.MaxFiles {
		c.MaxFiles = len(pats)
	}
	return nil
}

// PathError explains why a path could not be opened, in terms a person can act on.
func PathError(path string, err error) error {
	switch {
	case errors.Is(err, iofs.ErrNotExist):
		return fmt.Errorf("cannot find %q (check the path)", path)
	case errors.Is(err, iofs.ErrPermission):
		return fmt.Errorf("cannot read %q: permission denied", path)
	}
	var pe *iofs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return fmt.Errorf("cannot open %q: %v", path, err)
}
