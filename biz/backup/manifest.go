package backup

import (
	"encoding/json"
	"time"
)

// Format identifies the first stable backup package layout.
const (
	PackageFormat = "rainbow-business-backup-v1"
	SchemaVersion = 4

	ManifestEntry   = "manifest.json"
	DatabaseEntry   = "database/business.sqlite"
	UploadsPrefix   = "uploads/"
	SnapshotsPrefix = "release-snapshots/"
	assetsDirName   = "assets"
	assetBinSuffix  = ".bin"
)

// Producer identifies the application build that created the package.
type Producer struct {
	AppVersion string `json:"app_version"`
	GitCommit  string `json:"git_commit"`
}

// FileEntry is one file actually present in the ZIP.
type FileEntry struct {
	Path   string `json:"path"`
	Size   string `json:"size"`
	Sha256 string `json:"sha256"`
}

// Manifest is the package index. Its own digest is carried in the job record,
// never inside the manifest, to avoid a self-referential hash.
type Manifest struct {
	Format        string      `json:"format"`
	BackupID      string      `json:"backup_id"`
	CreatedAt     string      `json:"created_at"`
	ConsistentAt  string      `json:"consistent_at"`
	Producer      Producer    `json:"producer"`
	SchemaVersion int         `json:"schema_version"`
	Counts        Counts      `json:"counts"`
	Files         []FileEntry `json:"files"`
	Warnings      []string    `json:"warnings"`
}

func newManifest(backupID, consistentAt string, producer Producer, counts Counts,
	files []FileEntry, warnings []string) *Manifest {
	if files == nil {
		files = []FileEntry{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return &Manifest{
		Format:        PackageFormat,
		BackupID:      backupID,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		ConsistentAt:  consistentAt,
		Producer:      producer,
		SchemaVersion: SchemaVersion,
		Counts:        counts,
		Files:         files,
		Warnings:      warnings,
	}
}

func (m *Manifest) encode() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

func (m *Manifest) fileMap() map[string]FileEntry {
	result := make(map[string]FileEntry, len(m.Files))
	for _, file := range m.Files {
		result[file.Path] = file
	}
	return result
}

func releaseArchivePath(snapshotID, assetID string) string {
	return SnapshotsPrefix + snapshotID + "/" + assetsDirName + "/" + assetID + assetBinSuffix
}

func validTime(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && !parsed.IsZero()
}
