//go:build !windows

package backup

import "os"

// syncDir makes a directory rename durable.
func syncDir(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return nil
	}
	defer handle.Close()
	return handle.Sync()
}
