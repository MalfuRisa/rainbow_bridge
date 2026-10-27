//go:build windows

package backup

import (
	"path/filepath"
	"strings"
)

// sameVolume compares drive letters on Windows. UNC paths keep their share
// component so two directories on different shares are never considered equal.
func sameVolume(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}
