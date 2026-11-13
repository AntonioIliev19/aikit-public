package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/security"
)

func TestScannerRetrievalSamePolicy(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{".gitignore": "hidden/\n", "hidden/x.ts": "forbidden", "safe/x.ts": "safe", "safe/.conmeshignore": "private.ts\n", "safe/private.ts": "forbidden", "node_modules/x.ts": "forbidden", "secret.ts": "api_key=longSyntheticValue123456"}
	for name, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(full), 0700)
		if err := os.WriteFile(full, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := security.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	s, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range s.Files {
		if strings.Contains(name, "hidden/") || strings.Contains(name, "node_modules/") || name == "safe/private.ts" {
			t.Fatal("scanner bypassed policy", name)
		}
	}
	for _, name := range []string{"hidden/x.ts", "safe/private.ts", "node_modules/x.ts", "secret.ts", "../x", "safe/x.ts:stream", `safe\x.ts`} {
		if _, _, err := AuthorizedRead(root, name); err == nil {
			t.Fatal("retrieval bypassed policy", name)
		}
	}
	if _, _, err := AuthorizedRead(root, "safe/x.ts"); err != nil {
		t.Fatal(err)
	}
	if r := s.SkipReasons; r.Total() != s.Skipped || r.Ignored != 2 || r.HardDenied != 1 {
		t.Fatalf("skip reasons %+v for %d skipped", r, s.Skipped)
	}
	_, _, err = AuthorizedRead(root, "secret.ts")
	if !errors.Is(err, ErrSensitive) || !errors.Is(err, security.ErrDenied) || err.Error() != security.ErrDenied.Error() {
		t.Fatal("sensitive content reason", err)
	}
	os.WriteFile(filepath.Join(dir, "safe", "bin.ts"), []byte("a\x00b"), 0600)
	_, _, err = AuthorizedRead(root, "safe/bin.ts")
	if !errors.Is(err, ErrBinary) || !errors.Is(err, security.ErrDenied) || errors.Is(err, ErrSensitive) {
		t.Fatal("binary content reason", err)
	}
	var counted SkipReasons
	counted.CountReadError(ErrSensitive)
	counted.CountReadError(ErrBinary)
	counted.CountReadError(security.ErrDenied)
	if counted != (SkipReasons{SensitiveContent: 1, Binary: 1, Unreadable: 1}) {
		t.Fatalf("read error classification %+v", counted)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal("scan cancellation", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.ts")
	os.WriteFile(outside, []byte("forbidden"), 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "escape.ts")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	s, err = Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range s.Files {
		if name == "escape.ts" {
			t.Fatal("symlink scanned")
		}
	}
	if _, _, err := AuthorizedRead(root, "escape.ts"); err == nil {
		t.Fatal("symlink retrieved")
	}
}

func TestTraversalDepthLimit(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(strings.Repeat("a/", MaxDepth+1))), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := security.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := Scan(context.Background(), root); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded traversal depth", err)
	}
}
