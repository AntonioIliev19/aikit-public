package index

import (
	"context"
	"errors"
	"io/fs"

	"github.com/AntonioIliev19/aikit-public/internal/embeddings"
	"github.com/AntonioIliev19/aikit-public/internal/repository"
	"github.com/AntonioIliev19/aikit-public/internal/security"
)

// pruneSemantic removes logically orphaned vectors after a lexical commit. Only
// cached digests are visited; source lookups still depend on the current index.
func (c *Core) pruneSemantic(ctx context.Context) error {
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return security.ErrDenied
	}
	defer root.Close()
	if _, err := security.PrivateFile(root, "semantic.sqlite", false); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	cache, err := c.semanticDB(ctx, true)
	if errors.Is(err, embeddings.ErrUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	defer cache.Close()
	base, err := c.open(ctx, false)
	if err != nil {
		return err
	}
	defer base.Close()
	rows, err := cache.QueryContext(ctx, "SELECT DISTINCT substr(digest,1,65) FROM vectors LIMIT ?", maxCachedVectors+1)
	if err != nil {
		return ErrState
	}
	digests := []string{}
	for rows.Next() {
		var digest string
		if rows.Scan(&digest) != nil || len(digest) != 64 || len(digests) == maxCachedVectors {
			rows.Close()
			return ErrState
		}
		digests = append(digests, digest)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return ErrState
	}
	for _, digest := range digests {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		candidates, err := base.QueryContext(ctx, "SELECT substr(files.path,1,4097),substr(files.digest,1,65) FROM chunks JOIN files ON files.id=chunks.file_id WHERE chunks.digest=? LIMIT 8", digest)
		if err != nil {
			return ErrState
		}
		live := false
		for candidates.Next() {
			var path, fileDigest string
			if candidates.Scan(&path, &fileDigest) != nil {
				candidates.Close()
				return ErrState
			}
			if len(path) > 4096 || len(fileDigest) != 64 {
				candidates.Close()
				return ErrState
			}
			if _, current, err := repository.AuthorizedRead(root, path); err == nil && current == fileDigest {
				live = true
				break
			}
		}
		err = candidates.Err()
		candidates.Close()
		if err != nil {
			return ErrState
		}
		if !live {
			if _, err := cache.ExecContext(ctx, "DELETE FROM vectors WHERE digest=?", digest); err != nil {
				return ErrState
			}
		}
	}
	return nil
}
