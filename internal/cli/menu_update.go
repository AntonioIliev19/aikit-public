package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/security"
	"github.com/AntonioIliev19/conmesh/internal/update"
)

const updateCacheName = "menu-update.json"

type checkedRelease struct {
	Version string
	Checked time.Time
}

type menuUpdates struct {
	latest func(context.Context) (string, error)
	cached checkedRelease
	dir    string
}

func loadMenuUpdates() menuUpdates {
	client, err := update.New()
	if err != nil {
		return menuUpdates{}
	}
	dir, _ := config.GlobalDir()
	m := menuUpdates{latest: client.Latest, dir: dir}
	if dir == "" {
		return m
	}
	if root, err := security.OpenRoot(dir); err == nil {
		defer root.Close()
		if raw, err := security.ReadFileBounded(root, updateCacheName, 1024); err == nil {
			var result checkedRelease
			if json.Unmarshal(raw, &result) == nil && !result.Checked.After(time.Now()) {
				m.cached = result
			}
		}
	}
	return m
}

func (m menuUpdates) refresh(ctx context.Context) (checkedRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := m.latest(ctx)
	if err != nil {
		return checkedRelease{}, err
	}
	result := checkedRelease{Version: version, Checked: time.Now()}
	if m.dir != "" {
		m.save(result)
	}
	return result, nil
}

func (m menuUpdates) save(result checkedRelease) {
	if security.EnsurePrivateDirectory(m.dir) != nil {
		return
	}
	root, err := security.OpenRoot(m.dir)
	if err != nil {
		return
	}
	defer root.Close()
	name := ".menu-update-" + rand.Text()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer root.Remove(name)
	writeErr := json.NewEncoder(f).Encode(result)
	closeErr := f.Close()
	if writeErr == nil && closeErr == nil {
		_ = root.Rename(name, updateCacheName)
	}
}

func releaseStatus(installed string, result checkedRelease) string {
	if result.Checked.IsZero() || !update.Newer(result.Version, "v0.0.0") && result.Version != "v0.0.0" {
		return "No verified release check yet."
	}
	status := "Latest verified release: " + result.Version
	if update.Newer(result.Version, installed) {
		status = "Update available: " + result.Version
	}
	return fmt.Sprintf("%s (checked %s)", status, result.Checked.Local().Format("2006-01-02 15:04"))
}

func (m menuUpdates) install(ctx context.Context, out io.Writer, installed string, confirm func(string) bool, run func([]string)) error {
	if m.latest == nil {
		_, err := fmt.Fprintln(out, "Self-update requires a release build with a trusted signing key.")
		return err
	}
	result, err := m.refresh(ctx)
	if err != nil {
		_, err = fmt.Fprintln(out, "Could not verify the latest release.", releaseStatus(installed, m.cached))
		return err
	}
	if _, err := fmt.Fprintln(out, releaseStatus(installed, result)); err != nil {
		return err
	}
	if !update.Newer(result.Version, installed) {
		_, err := fmt.Fprintf(out, "You're up to date on %s.\n\n", installed)
		return err
	}
	if !confirm("Install this verified update and close the menu?") {
		return nil
	}
	run([]string{"update", "--version", result.Version})
	return errMenuExit
}

var errMenuExit = errors.New("menu exit")
