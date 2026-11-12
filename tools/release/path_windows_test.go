//go:build windows

package main

import "testing"

func TestExternalKeyCrossVolumeContainment(t *testing.T) {
	for _, tc := range []struct {
		repo, key string
		outside   bool
	}{
		{`F:\AIKIT`, `C:\Users\Owner\release-signing`, true},
		{`F:\AIKIT`, `f:\aikit\release-signing`, false},
		{`F:\AIKIT`, `F:\AIKIT\nested\release-signing`, false},
		{`F:\AIKIT`, `F:\AIKIT-neighbor\release-signing`, true},
	} {
		if got := outsideRepository(tc.repo, tc.key); got != tc.outside {
			t.Fatalf("cross-volume/same-volume containment=%t want %t", got, tc.outside)
		}
	}
}
