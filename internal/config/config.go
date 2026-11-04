// Package config resolves logi's settings. Precedence:
// CLI arguments > environment > config file > defaults.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the fully resolved configuration.
type Config struct {
	Path string // the log file to open

	Follow      bool
	Backlog     int64 // live: bytes before EOF to load at start; <0 = whole file
	Buffer      int   // live ring capacity in records
	BufferBytes int64 // live ring byte budget (0 = unlimited)

	Parser    string // "auto" or a profile name
	Multiline string // "auto" | "off"
	Plugins   map[string]bool

	MaxRecordBytes int
	MaxLines       int

	BatchInterval    time.Duration // UI notification batching
	IdleFlush        time.Duration
	ForcePoll        bool
	MaxFilterMatches int
	StaticCounts     bool // static mode: count records per severity in the background for the status bar

	Color string // auto | always | never

	Glob     string // directory mode: comma separated file patterns ("" = all log-like files)
	DirMode  string // directory mode: latest | all
	MaxFiles int    // directory mode "all": files followed at once

	// SourceMarkers adds a shape in front of the file name in the source column
	// when more files are followed than there are distinct colours (off by
	// default; see --source-markers).
	SourceMarkers bool

	// SourceGutter controls the file-colour bar in the narrow gutter in front of
	// the row number (the bookmark cell): "auto" (default) shows it whenever the
	// source column is hidden, which it is by default, so the message gets the
	// column's width; "on" shows it always; "off" never.
	SourceGutter string

	// Lines (with LinesSet) starts a live view with only the last N lines of the
	// file (-n/--lines); it implies Follow. Resolved to a byte backlog at start.
	Lines    int
	LinesSet bool

	// Since and Until restrict the view to a time window (--since/--until); they
	// are added to Filter as since:/until: terms.
	Since, Until string

	// Stdin reads standard input (logi -, or piped input detected by main).
	Stdin bool

	HideColumns []string // columns hidden at start
	ShowColumns []string // columns forced visible at start (e.g. source)

	Filter     string // initial filter
	SearchMode string // literal | regex | field

	Keys map[string][]string // action -> keys (overrides defaults)

	ConfigFile  string
	ShowVersion bool
	ListParsers bool
}

// Defaults returns the built-in defaults.
func Defaults() Config {
	return Config{
		Backlog:          8 << 20,
		Buffer:           50000,
		BufferBytes:      256 << 20,
		Parser:           "auto",
		Multiline:        "auto",
		Plugins:          map[string]bool{},
		MaxRecordBytes:   64 << 10,
		MaxLines:         2000,
		BatchInterval:    25 * time.Millisecond,
		IdleFlush:        300 * time.Millisecond,
		MaxFilterMatches: 1_000_000,
		StaticCounts:     true,
		SearchMode:       "literal",
		Color:            "auto",
		DirMode:          "latest",
		MaxFiles:         32,
		SourceGutter:     "auto",
		Keys:             map[string][]string{},
	}
}

// Load resolves configuration from args (without the program name) and env.
// stdout/stderr receive usage and errors from flag parsing.
func Load(args []string, getenv func(string) string, stderr io.Writer) (Config, error) {
	cfg := Defaults()

	// 1) config file (explicit path is scanned from args/env first)
	file := scanConfigPath(args, getenv)
	explicit := file != ""
	if file == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			file = filepath.Join(dir, "logi", "config.yaml")
		}
	}
	if file != "" {
		data, err := os.ReadFile(file)
		switch {
		case err == nil:
			m, perr := parseYAML(string(data))
			if perr != nil {
				return cfg, fmt.Errorf("%s: %v", file, perr)
			}
			if err := applyMap(&cfg, m); err != nil {
				return cfg, fmt.Errorf("%s: %v", file, err)
			}
			cfg.ConfigFile = file
		case explicit || !errors.Is(err, os.ErrNotExist):
			if explicit {
				return cfg, err
			}
		}
	}

	// 2) environment
	if err := applyEnv(&cfg, getenv); err != nil {
		return cfg, err
	}

	// 3) command line
	if err := applyFlags(&cfg, args, stderr); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func scanConfigPath(args []string, getenv func(string) string) string {
	for i, a := range args {
		if a == "--config" || a == "-config" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return getenv("LOGI_CONFIG")
}
