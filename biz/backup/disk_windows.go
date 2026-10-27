//go:build windows

package backup

import "golang.org/x/sys/windows"

func diskFree(path string) (int64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeAvailable, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &freeAvailable, &total, &totalFree); err != nil {
		return 0, err
	}
	return int64(freeAvailable), nil
}
