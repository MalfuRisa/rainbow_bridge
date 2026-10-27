package backup

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// writeFileAtomic writes a durable control record: temporary file in the same
// directory, fsync, atomic rename and (where supported) a directory sync.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	temporary := path + ".tmp-" + hex.EncodeToString(suffix)
	handle, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = handle.Close()
		_ = os.Remove(temporary)
	}
	if _, err := handle.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := handle.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := handle.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDir(directory)
}
