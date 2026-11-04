package input

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileEntry describes a candidate log file in a directory.
type FileEntry struct {
	Name    string
	Path    string
	Size    int64
	ModTime time.Time
}

// FileFilter decides which directory entries are log files.
//
// Always excluded: hidden files, sub-directories and compressed archives.
// With no explicit patterns, numbered rotation leftovers ("app.log.1") are
// excluded too; explicit patterns (comma separated, e.g. "*.log,*.txt") replace
// that rule so the user stays in control.
type FileFilter struct {
	patterns []string
}

var archiveExts = map[string]bool{".gz": true, ".zip": true, ".bz2": true, ".xz": true, ".zst": true,
	".7z": true, ".rar": true, ".tar": true, ".tgz": true, ".lz4": true}

// NewFileFilter validates and compiles a comma separated glob list.
func NewFileFilter(glob string) (*FileFilter, error) {
	f := &FileFilter{}
	for _, p := range strings.Split(glob, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if _, err := filepath.Match(p, "x"); err != nil {
			return nil, fmt.Errorf("bad glob %q: %v", p, err)
		}
		f.patterns = append(f.patterns, strings.ToLower(p))
	}
	return f, nil
}

func numericSuffix(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 || i == len(name)-1 {
		return false
	}
	for _, c := range name[i+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Match reports whether a file name is a log file under this filter.
func (f *FileFilter) Match(name string) bool {
	if name == "" || name[0] == '.' || archiveExts[strings.ToLower(filepath.Ext(name))] {
		return false
	}
	if len(f.patterns) == 0 {
		return !numericSuffix(name)
	}
	lower := strings.ToLower(name)
	for _, p := range f.patterns {
		if ok, _ := filepath.Match(p, lower); ok {
			return true
		}
	}
	return false
}

// ListFiles returns the matching regular files of dir, newest first.
func ListFiles(dir string, f *FileFilter) ([]FileEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []FileEntry
	for _, de := range des {
		if !f.Match(de.Name()) {
			continue
		}
		p := filepath.Join(dir, de.Name())
		var fi os.FileInfo
		if de.Type().IsRegular() {
			// Plain files: the entry already knows its type, and Info avoids
			// building and resolving a path per file on every rescan.
			if fi, err = de.Info(); err != nil {
				continue
			}
		} else if de.Type()&os.ModeSymlink != 0 {
			if fi, err = os.Stat(p); err != nil || !fi.Mode().IsRegular() { // follow symlinks
				continue
			}
		} else {
			continue // directories, sockets, devices
		}
		out = append(out, FileEntry{Name: de.Name(), Path: p, Size: fi.Size(), ModTime: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].ModTime.After(out[j].ModTime)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}
