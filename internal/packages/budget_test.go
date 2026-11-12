package packages

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGitStagingBudget(t *testing.T) {
	dir := physicalTemp(t)
	f, err := os.Create(filepath.Join(dir, "pack"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxGitStagingBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if stagingBudget(dir, MaxGitStagingBytes, 10) == nil {
		t.Fatal("oversize staging accepted")
	}
	os.Remove(filepath.Join(dir, "pack"))
	for _, n := range []string{"a", "b"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0600)
	}
	if stagingBudget(dir, 100, 1) == nil {
		t.Fatal("entry budget ignored")
	}
	if err := stagingBudget(dir, 100, 2); err != nil {
		t.Fatal(err)
	}
}

func TestGitStagingBudgetRejectsLinkedRoot(t *testing.T) {
	dir := physicalTemp(t)
	alias := filepath.Join(physicalTemp(t), "linked-staging")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skip("directory symlink creation unavailable")
	}
	if !errors.Is(stagingBudget(alias, 100, 2), errGitBudget) {
		t.Fatal("linked staging root accepted")
	}
	if err := stagingBudget(dir, 100, 2); err != nil {
		t.Fatal(err)
	}
}
func TestFetchCancelledOnGrowthAndCleaned(t *testing.T) {
	var staged string
	git := func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		staged = dir
		if args[0] == "init" {
			return nil, nil
		}
		f, err := os.Create(filepath.Join(dir, "oversized-pack"))
		if err != nil {
			return nil, err
		}
		err = f.Truncate(MaxGitStagingBytes + 1)
		f.Close()
		if err != nil {
			return nil, err
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := loadGit(ctx, "https://example.invalid/repo", "skill", "v1", git); !errors.Is(err, errGitBudget) {
		t.Fatalf("want budget cancellation, got %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("Git staging not cleaned")
	}
}
