package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// filePlan is a verified source file and the logical ZIP path it maps to.
type filePlan struct {
	ArchivePath string
	SourcePath  string
	Size        int64
	Sha256      string
}

// collectFilePlans derives the required file set from business metadata. Any
// missing file, size mismatch or digest mismatch fails the whole backup.
func collectFilePlans(ctx context.Context, db *gorm.DB, layout *Layout, limits Limits) ([]filePlan, []string, error) {
	plans := make([]filePlan, 0)
	seenArchive := make(map[string]bool)
	seenSource := make(map[string]bool)

	assetPlans, err := collectAssetPlans(ctx, db, layout)
	if err != nil {
		return nil, nil, err
	}
	plans = append(plans, assetPlans...)

	snapshotPlans, err := collectSnapshotPlans(ctx, db, layout)
	if err != nil {
		return nil, nil, err
	}
	plans = append(plans, snapshotPlans...)

	sort.Slice(plans, func(i, j int) bool { return plans[i].ArchivePath < plans[j].ArchivePath })
	var total int64
	for _, plan := range plans {
		if seenArchive[plan.ArchivePath] {
			return nil, nil, fmt.Errorf("duplicate archive path %s", plan.ArchivePath)
		}
		seenArchive[plan.ArchivePath] = true
		seenSource[plan.SourcePath] = true
		total += plan.Size
	}
	if len(plans) > limits.MaxFiles {
		return nil, nil, limitError("too many files in backup")
	}
	if total > limits.MaxExtractedBytes {
		return nil, nil, limitError("backup files exceed the extracted size limit")
	}

	warnings := make([]string, 0)
	unmanaged := countUnmanagedFiles(layout, seenSource)
	if unmanaged > 0 {
		warnings = append(warnings, fmt.Sprintf("unmanaged_files_ignored:%d", unmanaged))
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	if external := externalReferenceCount(ctx, sqlDB); external > 0 {
		warnings = append(warnings, fmt.Sprintf("external_references_not_included:%d", external))
	}
	return plans, warnings, nil
}

func collectAssetPlans(ctx context.Context, db *gorm.DB, layout *Layout) ([]filePlan, error) {
	rows, err := db.WithContext(ctx).Raw("SELECT file_id, path, file_size FROM asset").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]filePlan, 0)
	for rows.Next() {
		var fileID, storedPath string
		var expectedSize int64
		if err := rows.Scan(&fileID, &storedPath, &expectedSize); err != nil {
			return nil, err
		}
		relative := normalizeStoredRelative(storedPath)
		if !strings.HasPrefix(relative, "uploads/") {
			return nil, fmt.Errorf("asset %s is outside the local uploads root", fileID)
		}
		source, ok := joinWithin(layout.BusinessRoot, relative)
		if !ok {
			return nil, fmt.Errorf("asset %s has an invalid managed path", fileID)
		}
		size, digest, err := verifyRegularFile(source)
		if err != nil {
			return nil, fmt.Errorf("asset %s: %w", fileID, err)
		}
		if expectedSize > 0 && size != expectedSize {
			return nil, fmt.Errorf("asset %s length does not match metadata", fileID)
		}
		plans = append(plans, filePlan{
			ArchivePath: relative,
			SourcePath:  source,
			Size:        size,
			Sha256:      digest,
		})
	}
	return plans, rows.Err()
}

func collectSnapshotPlans(ctx context.Context, db *gorm.DB, layout *Layout) ([]filePlan, error) {
	rows, err := db.WithContext(ctx).Raw(
		`SELECT a.snapshot_id, a.asset_id, a.size, a.sha256
		 FROM config_release_asset a
		 JOIN config_release_version v ON v.snapshot_id = a.snapshot_id`).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]filePlan, 0)
	for rows.Next() {
		var snapshotID, assetID, expectedSha string
		var expectedSize int64
		if err := rows.Scan(&snapshotID, &assetID, &expectedSize, &expectedSha); err != nil {
			return nil, err
		}
		if !safeSegment(snapshotID) || !safeSegment(assetID) {
			return nil, fmt.Errorf("release snapshot contains an unsafe identifier")
		}
		source := filepath.Join(layout.ReleaseDir, snapshotID, assetsDirName, assetID+assetBinSuffix)
		if !isWithin(layout.ReleaseDir, source) {
			return nil, fmt.Errorf("release asset path escapes the snapshot root")
		}
		size, digest, err := verifyRegularFile(source)
		if err != nil {
			return nil, fmt.Errorf("release asset %s/%s: %w", snapshotID, assetID, err)
		}
		if size != expectedSize || digest != expectedSha {
			return nil, fmt.Errorf("release asset %s/%s does not match its manifest row", snapshotID, assetID)
		}
		plans = append(plans, filePlan{
			ArchivePath: releaseArchivePath(snapshotID, assetID),
			SourcePath:  source,
			Size:        size,
			Sha256:      digest,
		})
	}
	return plans, rows.Err()
}

func countUnmanagedFiles(layout *Layout, managed map[string]bool) int {
	count := 0
	for _, root := range []string{layout.UploadsDir, layout.ReleaseDir} {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == ".staging" {
					return filepath.SkipDir
				}
				return nil
			}
			if !managed[path] {
				count++
			}
			return nil
		})
	}
	return count
}

// copyPlansToStaging copies every verified source file into the private
// staging directory, re-checking size and digest after the copy.
func copyPlansToStaging(ctx context.Context, plans []filePlan, stagingRoot string) error {
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, ok := joinWithin(stagingRoot, plan.ArchivePath)
		if !ok {
			return fmt.Errorf("invalid staging target")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := copyFileVerified(plan.SourcePath, target, plan.Size, plan.Sha256, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func copyFileVerified(sourcePath, targetPath string, expectedSize int64, expectedSha string, mode os.FileMode) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(target, hasher), source)
	syncErr := target.Sync()
	closeErr := target.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(targetPath)
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if written != expectedSize || digest != expectedSha {
		_ = os.Remove(targetPath)
		return fmt.Errorf("file verification failed after copy")
	}
	return nil
}

func verifyRegularFile(path string) (int64, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, "", fmt.Errorf("managed path is not a regular file")
	}
	handle, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer handle.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, handle)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), nil
}

// normalizeStoredRelative turns the platform-native path stored by older
// uploads into a manifest-safe slash path.
func normalizeStoredRelative(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	cleaned := strings.TrimPrefix(trimmed, "./")
	return cleaned
}

func safeSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	return !strings.ContainsAny(value, "/\\\x00")
}

func buildFileEntries(plans []filePlan) []FileEntry {
	entries := make([]FileEntry, 0, len(plans)+1)
	for _, plan := range plans {
		entries = append(entries, FileEntry{
			Path:   plan.ArchivePath,
			Size:   decimalString(plan.Size),
			Sha256: plan.Sha256,
		})
	}
	return entries
}
