//go:build windows

package main

import "testing"

func TestExternalKeyCrossVolumeContainment(t *testing.T) {
	for _, tc := range []struct {
		repo, key string
		outside   bool
	}{
		{`F:\CONMESH`, `C:\Users\Owner\release-signing`, true},
		{`F:\CONMESH`, `f:\conmesh\release-signing`, false},
		{`F:\CONMESH`, `F:\CONMESH\nested\release-signing`, false},
		{`F:\CONMESH`, `F:\CONMESH-neighbor\release-signing`, true},
	} {
		if got := outsideRepository(tc.repo, tc.key); got != tc.outside {
			t.Fatalf("cross-volume/same-volume containment=%t want %t", got, tc.outside)
		}
	}
}
