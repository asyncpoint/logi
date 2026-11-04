//go:build !windows

package main

import "os"

// openTerminal opens the controlling terminal for keyboard input, used when
// standard input carries the log (kubectl logs ... | logi).
func openTerminal() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }

// attachTerminalAsStdin makes the terminal the process's standard input, so the
// interface starts exactly as it does when logi is launched from a terminal. The
// pipe that carries the log must have been saved beforehand.
func attachTerminalAsStdin(tty *os.File) { os.Stdin = tty }
