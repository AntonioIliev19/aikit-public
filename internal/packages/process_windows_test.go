//go:build windows

package packages

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsSuspendedBeforeJobAttachment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growth")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestProcessChild$")
	cmd.Env = append(os.Environ(), "AIKIT_PROCESS_HELPER=parent", "AIKIT_PROCESS_GROWTH="+path)
	cmd.WaitDelay = time.Second
	before := false
	err := runContainedBeforeAttach(ctx, cmd, func() {
		before = true
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("process ran before job attachment")
		}
		cancel()
	})
	if !before || err == nil {
		t.Fatal("expected contained cancellation after suspended attachment")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cancelled suspended process created descendants")
	}
}
