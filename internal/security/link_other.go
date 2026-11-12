//go:build !windows

package security

import "io/fs"

func IsLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }
