package packages

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/security"
)

const MaxGitStagingBytes int64 = 128 << 20
const MaxGitStagingEntries = 32768

var errGitBudget = errors.New("Git staging byte or entry budget exceeded; transfer cancelled")

// stagingBudget is a monitored logical-byte budget, not a filesystem quota.
// A writer may overshoot between polls; no maximum overshoot is guaranteed.
func stagingBudget(dir string, bytes int64, entries int) error {
	root, err := security.OpenRoot(dir)
	if err != nil {
		return errGitBudget
	}
	defer root.Close()
	var total int64
	visited := 0
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		if depth > MaxDepth {
			return errGitBudget
		}
		f, err := root.Open(name)
		if err != nil {
			return errGitBudget
		}
		defer f.Close()
		for {
			batch, readErr := f.ReadDir(64)
			for _, e := range batch {
				visited++
				if visited > entries {
					return errGitBudget
				}
				path := e.Name()
				if name != "." {
					path = name + "/" + path
				}
				info, err := root.Lstat(path)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil || security.CheckEntry(info) != nil {
					return errGitBudget
				}
				if info.IsDir() {
					if err = walk(path, depth+1); err != nil {
						return err
					}
				} else {
					total += info.Size()
					if total > bytes {
						return errGitBudget
					}
				}
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					break
				}
				return errGitBudget
			}
		}
		return nil
	}
	return walk(".", 0)
}
func boundedFetch(ctx context.Context, dir string, git gitRunner, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() { data, err := git(ctx, dir, args...); done <- result{data, err} }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case r := <-done:
			if err := stagingBudget(dir, MaxGitStagingBytes, MaxGitStagingEntries); err != nil {
				return nil, err
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return r.data, r.err
		case <-ticker.C:
			if err := stagingBudget(dir, MaxGitStagingBytes, MaxGitStagingEntries); err != nil {
				cancel()
				<-done
				return nil, err
			}
		case <-ctx.Done():
			<-done
			return nil, ctx.Err()
		}
	}
}
