package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeCloser leaves the caller's stdout open; only protocol frames go there.
type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }

func serveMCP(ctx context.Context, args []string, version, cwd string, input io.ReadCloser, output io.Writer) error {
	if len(args) == 0 || args[0] != "serve" {
		return errUsage
	}
	opts, err := parseOptions(args[1:], map[string]bool{"project": true})
	if err != nil || len(opts.positionals) != 0 {
		return errUsage
	}
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return errors.New("working directory unavailable")
		}
	}
	var project string
	if opts.has("project") {
		project = opts.values["project"]
		if !filepath.IsAbs(project) {
			project = filepath.Join(cwd, project)
		}
		project, err = filepath.Abs(project)
		if err != nil {
			return errors.New("MCP project root unavailable")
		}
		if _, err := config.Load(project); err != nil {
			return errors.New("MCP project configuration unavailable")
		}
	} else {
		project, _, err = config.Find(cwd)
		if err != nil {
			return err
		}
	}
	return mcp.Serve(ctx, project, version, &sdk.IOTransport{Reader: input, Writer: writeCloser{output}, MaxLineLength: 16 << 10})
}
