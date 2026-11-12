//go:build linux || darwin

package benchmark

import (
	"runtime"
	"syscall"
)

func peakRSS() uint64 {
	var r syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &r) != nil {
		return 0
	}
	n := uint64(r.Maxrss)
	if runtime.GOOS == "linux" {
		n *= 1024
	}
	return n
}
