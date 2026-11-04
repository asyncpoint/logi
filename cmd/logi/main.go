// Command logi is a lightweight, terminal-first log inspector.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/plugins"
	"github.com/asyncpoint/logi/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=v1.0.0".
var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(args, os.Getenv, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "logi:", err)
		return 2
	}
	if cfg.ShowVersion {
		fmt.Fprintln(stdout, "logi", version)
		return 0
	}
	if cfg.ListParsers {
		reg, err := plugins.BuildRegistry(cfg.Plugins)
		if err != nil {
			fmt.Fprintln(stderr, "logi:", err)
			return 2
		}
		for _, name := range reg.Names() {
			p, _ := reg.Get(name)
			fmt.Fprintf(stdout, "%-10s %s\n", name, p.Description)
		}
		return 0
	}
	if cfg.Path == "" && !cfg.Stdin {
		if !stdinIsPiped() {
			fmt.Fprintln(stderr, "logi: no file given\nusage: logi [options] <file|directory>   or   <command> | logi   (see logi --help)")
			return 2
		}
		cfg.Stdin, cfg.Follow = true, true // something is piped in: read it
	}

	// Standard input carries the log, so the keyboard has to come from the
	// terminal. Save the pipe for the session, then make the terminal the process's
	// standard input: the interface then starts exactly as it does for a normal
	// terminal launch (raw mode, key and mouse handling included).
	var pipe io.Reader
	if cfg.Stdin {
		pipe = os.Stdin
		tty, err := openTerminal()
		if err != nil {
			fmt.Fprintln(stderr, "logi: reading standard input needs a terminal for the keyboard:", err)
			return 1
		}
		defer tty.Close()
		attachTerminalAsStdin(tty)
	}

	var sess *app.Session
	if cfg.Stdin {
		sess, err = app.NewSessionFromReader(cfg, pipe)
	} else {
		sess, err = app.NewSession(cfg)
	}
	if err != nil {
		fmt.Fprintln(stderr, "logi:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sess.Start(ctx)

	final, err := tea.NewProgram(tui.New(ctx, sess, cfg), programOptions(cfg)...).Run()
	if m, ok := final.(*tui.Model); ok {
		m.Session().Close()
	} else {
		sess.Close()
	}
	if err != nil {
		fmt.Fprintln(stderr, "logi:", err)
		return 1
	}
	return 0
}

// programOptions applies --color. "auto" keeps the renderer's own detection
// (which honours NO_COLOR and TERM); "always" guarantees at least 16-colour
// ANSI even where detection found none (dumb terminals, NO_COLOR, piped
// consoles) while keeping a better detected profile; "never" strips colour but
// keeps bold/reverse/underline so the interface stays usable.
func programOptions(cfg config.Config) []tea.ProgramOption {
	switch cfg.Color {
	case "always":
		if colorprofile.Detect(os.Stdout, os.Environ()) < colorprofile.ANSI {
			return []tea.ProgramOption{tea.WithColorProfile(colorprofile.ANSI)}
		}
	case "never":
		return []tea.ProgramOption{tea.WithColorProfile(colorprofile.ASCII)}
	}
	return nil
}
