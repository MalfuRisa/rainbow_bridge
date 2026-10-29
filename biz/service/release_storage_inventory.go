package service

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// Directory names only, no deletion, no traversal of untrusted symlinks. Legacy
// directories belonging to migrated versions are retained sources, not garbage.
type LocalSnapshotDirectoryInventory struct {
	Status      string
	Uncommitted []string
	Staging     []string
	Unexpected  []string
}

func (s *Service) inspectLocalSnapshotDirectories(rows []model.ConfigReleaseVersion) LocalSnapshotDirectoryInventory {
	view := LocalSnapshotDirectoryInventory{Status: "verified", Uncommitted: []string{}, Staging: []string{}, Unexpected: []string{}}
	local, err := s.existingReleaseStorage()
	if err != nil {
		view.Status = "root_unavailable"
		return view
	}
	known := map[string]bool{}
	for _, row := range rows {
		known[row.SnapshotID] = true
	}
	entries, err := os.ReadDir(local.root)
	if err != nil {
		view.Status = "directory_unreadable"
		return view
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			view.Unexpected = append(view.Unexpected, name)
			continue
		}
		if name == releaseStagingDirName {
			children, err := os.ReadDir(filepath.Join(local.root, name))
			if err != nil {
				view.Status = "staging_unreadable"
				continue
			}
			for _, child := range children {
				view.Staging = append(view.Staging, child.Name())
			}
			continue
		}
		if !known[name] {
			view.Uncommitted = append(view.Uncommitted, name)
		}
	}
	sort.Strings(view.Uncommitted)
	sort.Strings(view.Staging)
	sort.Strings(view.Unexpected)
	return view
}
