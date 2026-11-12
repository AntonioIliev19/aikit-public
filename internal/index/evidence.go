package index

import (
	"context"

	"github.com/AntonioIliev19/aikit-public/internal/repository"
	"github.com/AntonioIliev19/aikit-public/internal/security"
)

// checkEvidence closes the policy/digest window after optional runtime/process
// work, including lexical hits outside the bounded semantic candidate prefix.
func (c *Core) checkEvidence(ctx context.Context, evidence []Evidence) error {
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return security.ErrDenied
	}
	defer root.Close()
	for _, e := range evidence {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, digest, err := repository.AuthorizedRead(root, e.Path)
		if err != nil {
			return err
		}
		if len(e.Digest) != 64 || digest != e.Digest {
			return repository.ErrStale
		}
	}
	return nil
}
