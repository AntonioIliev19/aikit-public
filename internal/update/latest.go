package update

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func validVersion(version string) bool {
	return len(version) <= 40 && versionPattern.MatchString(version)
}

// Newer compares numeric stable version components without integer overflow.
func Newer(candidate, installed string) bool {
	if !validVersion(candidate) || !validVersion(installed) {
		return false
	}
	a := strings.Split(candidate[1:], ".")
	b := strings.Split(installed[1:], ".")
	for i := range a {
		x, y := strings.TrimLeft(a[i], "0"), strings.TrimLeft(b[i], "0")
		if len(x) != len(y) {
			return len(x) > len(y)
		}
		if order := cmp.Compare(x, y); order != 0 {
			return order > 0
		}
	}
	return false
}

// Latest returns a stable tag only after checking its signed platform manifest.
func (c *Client) Latest(ctx context.Context) (string, error) {
	data, err := c.get(ctx, c.latestURL, 1<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if json.Unmarshal(data, &release) != nil || release.Draft || release.Prerelease || !validVersion(release.Tag) {
		return "", errors.New("no valid stable release metadata")
	}
	if _, err := c.Check(ctx, release.Tag); err != nil {
		return "", err
	}
	return release.Tag, nil
}
