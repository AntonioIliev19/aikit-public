package packages

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessTreeCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growth")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestProcessChild$")
	cmd.Env = append(os.Environ(), "CONMESH_PROCESS_HELPER=parent", "CONMESH_PROCESS_GROWTH="+path)
	cmd.WaitDelay = time.Second
	done := make(chan error, 1)
	go func() { done <- runContained(ctx, cmd) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, err := os.Stat(path); err == nil && info.Size() > 2 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("descendant did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("contained process did not return")
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	last, err := os.Stat(path)
	if err != nil || first.Size() != last.Size() {
		t.Fatal("descendant continued writing after cancellation")
	}
}
func TestProcessChild(t *testing.T) {
	mode := os.Getenv("CONMESH_PROCESS_HELPER")
	if mode == "" {
		t.Skip("subprocess only")
	}
	if mode == "parent" {
		cmd := exec.Command(os.Args[0], "-test.run=TestProcessChild$")
		cmd.Env = append(os.Environ(), "CONMESH_PROCESS_HELPER=writer")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmd.Wait()
		return
	}
	f, err := os.OpenFile(os.Getenv("CONMESH_PROCESS_GROWTH"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for {
		if _, err = f.Write([]byte("x")); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
