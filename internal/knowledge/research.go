package knowledge

import (
	_ "embed"
	"os"

	"github.com/AntonioIliev19/aikit-public/internal/security"
)

//go:embed research.md
var ResearchSkill string

// Scaffold creates only an empty proposed-knowledge manifest, exclusively.
func (s Store) Scaffold() error {
	root, err := security.OpenRoot(s.Project)
	if err != nil {
		return security.ErrDenied
	}
	defer root.Close()
	if security.CheckPath(s.Project+"/.aikit/knowledge") != nil {
		return security.ErrDenied
	}
	if root.MkdirAll(".aikit/knowledge", 0700) != nil {
		return security.ErrDenied
	}
	f, err := root.OpenFile(ManifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrManifest
	}
	_, err = f.WriteString("{\"schema_version\":1,\"entries\":[]}\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return ErrManifest
	}
	return nil
}
