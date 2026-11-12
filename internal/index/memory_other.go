//go:build (!windows && !linux && !darwin) || (darwin && !cgo)

package index

// Free-memory probing is unavailable on this platform; fixed conservative caps apply.
func availableMemory() uint64 { return 0 }
