//go:build windows

package main

import (
	"os"
	"syscall"
)

// openTerminal opens the console for keyboard input, used when standard input
// carries the log (type app.log | logi).
func openTerminal() (*os.File, error) { return os.OpenFile("CONIN$", os.O_RDWR, 0) }

var procSetStdHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("SetStdHandle")

// stdInputHandle is STD_INPUT_HANDLE, (DWORD)-10.
const stdInputHandle = ^uintptr(9)

// attachTerminalAsStdin makes the console the process's standard input (the Go
// variable and the Windows standard handle), so the interface starts exactly as
// it does when logi is launched from a console. The pipe that carries the log
// must have been saved beforehand.
func attachTerminalAsStdin(tty *os.File) {
	os.Stdin = tty
	procSetStdHandle.Call(stdInputHandle, tty.Fd()) // best effort: os.Stdin is already switched
}
