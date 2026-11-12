package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

func isTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

func enableColor(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	return windows.GetConsoleMode(h, &mode) == nil && windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
