//go:build !windows

package backup

import (
	"os"
	"syscall"
)

// sameVolume compares the device id of the two paths so a rename can be
// treated as an atomic same-filesystem operation.
func sameVolume(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	statA, okA := infoA.Sys().(*syscall.Stat_t)
	statB, okB := infoB.Sys().(*syscall.Stat_t)
	if !okA || !okB {
		return false
	}
	return statA.Dev == statB.Dev
}
