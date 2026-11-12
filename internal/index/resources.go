package index

import (
	"context"
	"runtime/debug"
)

// Go's memory limit is process-global, so only one in-process index adjusts it.
var memoryProfile = make(chan struct{}, 1)

func resources(ctx context.Context, profile string) (int64, func(), error) {
	select {
	case memoryProfile <- struct{}{}:
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
	target := int64(4096 << 20)
	if profile == "eco" {
		target = 512 << 20
	} else if profile == "performance" {
		target = 8192 << 20
	}
	if available := availableMemory(); available > 0 && available/4 < uint64(target) {
		target = int64(available / 4)
	}
	if target < 32<<20 {
		target = 32 << 20
	}
	previous := debug.SetMemoryLimit(-1)
	if previous < target {
		target = previous
	}
	debug.SetMemoryLimit(target)
	return target, func() { debug.SetMemoryLimit(previous); <-memoryProfile }, nil
}
