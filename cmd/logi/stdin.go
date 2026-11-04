package main

import "os"

// stdinIsPiped reports whether standard input is a pipe or a redirected file
// rather than a terminal, i.e. whether something is being piped into logi.
func stdinIsPiped() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice == 0
}
