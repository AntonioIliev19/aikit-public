//go:build !windows && !linux && !darwin

package benchmark

func peakRSS() uint64 { return 0 }
