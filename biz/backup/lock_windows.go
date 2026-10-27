//go:build windows

package backup

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive, non-blocking whole-file lock. The handle stays
// open for the lifetime of the process; the OS releases it on exit.
func lockFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped)
	if err != nil {
		return fmt.Errorf("instance lock is held by another process: %w", err)
	}
	return nil
}

func unlockFile(file *os.File) {
	overlapped := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}
