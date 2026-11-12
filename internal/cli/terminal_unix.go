//go:build !windows

package cli

import (
	"os"

	"github.com/mattn/go-isatty"
)

func isTerminal(f *os.File) bool { return isatty.IsTerminal(f.Fd()) }
func enableColor(*os.File) bool  { return true }
