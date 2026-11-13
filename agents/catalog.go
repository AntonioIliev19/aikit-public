// Package agents embeds the predefined ConMesh packages so every build can
// offer them without a checkout or network access. Each directory is an
// ordinary package with conmesh-package.json, a LICENSE and its instructions.
package agents

import "embed"

// FS holds one directory per predefined package. Add a new package directory
// to this list as well; a test fails when a package directory is missing.
//
//go:embed angular-agent dotnet-agent repository-research
var FS embed.FS
