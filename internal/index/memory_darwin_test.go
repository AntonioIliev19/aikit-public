//go:build darwin && cgo

package index

import "testing"

func TestNativeAvailableMemory(t *testing.T) {
	if availableMemory() == 0 {
		t.Fatal("native Mach available-memory probe failed")
	}
}
