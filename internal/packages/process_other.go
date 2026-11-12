//go:build !windows && !linux && !darwin

package packages

import (
	"context"
	"errors"
	"os/exec"
)

func runContained(ctx context.Context, cmd *exec.Cmd) error {
	return errors.New("contained Git execution is unsupported on this platform")
}
