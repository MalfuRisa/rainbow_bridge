//go:build windows

package backup

// Windows cannot fsync a directory handle through os.File.Sync. The rename
// itself is still handled by the NTFS journal; backup packages additionally
// fsync the file before rename.
func syncDir(string) error { return nil }
