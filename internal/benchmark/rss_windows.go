//go:build windows

package benchmark

import (
	"syscall"
	"unsafe"
)

func peakRSS() uint64 {
	var counters struct {
		Size, Faults                                                                                 uint32
		PeakWorkingSet, WorkingSet, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	process := ^uintptr(0)
	proc := syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	ok, _, _ := proc.Call(process, uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if ok == 0 {
		return 0
	}
	return uint64(counters.PeakWorkingSet)
}
