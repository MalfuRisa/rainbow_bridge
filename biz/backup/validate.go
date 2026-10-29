package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/glebarez/go-sqlite"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
)

// PackageInfo is the validated content of a backup package.
type PackageInfo struct {
	Manifest      *Manifest
	PackageSHA256 string
	PackageSize   int64
	FileCount     int
	ExtractedSize int64
}

// inspectPackage validates the ZIP structure, the manifest and the business
// database, and extracts verified files into workDir for later candidate
// construction. Structural problems are returned as issues; I/O failures are
// returned as errors.
func inspectPackage(ctx context.Context, packagePath, workDir string, limits Limits) (*PackageInfo, []string, error) {
	info := &PackageInfo{}
	size, digest, err := verifyRegularFile(packagePath)
	if err != nil {
		return nil, nil, err
	}
	if size > limits.MaxPackageBytes {
		return nil, nil, limitError("backup package exceeds the size limit")
	}
	info.PackageSize = size
	info.PackageSHA256 = digest

	reader, err := zip.OpenReader(packagePath)
	if err != nil {
		return info, []string{"package_is_not_a_valid_zip"}, nil
	}
	defer reader.Close()

	entries, issues := indexZipEntries(ctx, &reader.Reader, limits)
	if len(issues) > 0 {
		return info, issues, nil
	}
	manifestEntry, ok := entries[ManifestEntry]
	if !ok {
		return info, []string{"manifest_missing"}, nil
	}
	manifest, issues := readManifest(manifestEntry, limits)
	if len(issues) > 0 {
		return info, issues, nil
	}
	info.Manifest = manifest
	info.FileCount = len(manifest.Files)

	issues = validateManifestLayout(manifest)
	if len(issues) > 0 {
		return info, issues, nil
	}
	issues = compareEntrySets(entries, manifest)
	if len(issues) > 0 {
		return info, issues, nil
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return nil, nil, err
	}
	extracted, issues, err := extractEntries(ctx, entries, manifest, workDir, limits)
	if err != nil {
		return nil, nil, err
	}
	info.ExtractedSize = extracted
	if len(issues) > 0 {
		return info, issues, nil
	}

	issues, err = validateExtractedDatabase(ctx, filepath.Join(workDir, filepath.FromSlash(DatabaseEntry)),
		manifest, limits)
	if err != nil {
		return nil, nil, err
	}
	return info, issues, nil
}

func indexZipEntries(ctx context.Context, reader *zip.Reader, limits Limits) (map[string]*zip.File, []string) {
	issues := make([]string, 0)
	entries := make(map[string]*zip.File, len(reader.File))
	seenFold := make(map[string]bool, len(reader.File))
	count := 0
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return entries, []string{"validation_cancelled"}
		}
		name := entry.Name
		if _, ok := safeRelativePath(name); !ok {
			issues = append(issues, "unsafe_entry_path")
			continue
		}
		folded := strings.ToLower(name)
		if seenFold[folded] {
			issues = append(issues, "duplicate_entry:"+name)
			continue
		}
		seenFold[folded] = true
		if entry.FileInfo().IsDir() {
			issues = append(issues, "directory_entry_not_allowed:"+name)
			continue
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && mode != 0) {
			issues = append(issues, "non_regular_entry:"+name)
			continue
		}
		if entry.UncompressedSize64 > uint64(limits.MaxExtractedBytes) {
			issues = append(issues, "entry_exceeds_size_limit:"+name)
			continue
		}
		count++
		if count > limits.MaxFiles+1 {
			issues = append(issues, "too_many_entries")
			continue
		}
		entries[name] = entry
	}
	return entries, issues
}

func readManifest(entry *zip.File, limits Limits) (*Manifest, []string) {
	if entry.UncompressedSize64 > uint64(limits.MaxManifestBytes) {
		return nil, []string{"manifest_too_large"}
	}
	handle, err := entry.Open()
	if err != nil {
		return nil, []string{"manifest_unreadable"}
	}
	defer handle.Close()
	payload, err := io.ReadAll(io.LimitReader(handle, limits.MaxManifestBytes+1))
	if err != nil || int64(len(payload)) > limits.MaxManifestBytes {
		return nil, []string{"manifest_too_large"}
	}
	var manifest Manifest
	if err := jsonUnmarshalStrict(payload, &manifest); err != nil {
		return nil, []string{"manifest_invalid_json"}
	}
	issues := make([]string, 0)
	if manifest.Format != PackageFormat {
		issues = append(issues, "unsupported_package_format")
	}
	if manifest.SchemaVersion < 1 || manifest.SchemaVersion > SchemaVersion {
		issues = append(issues, "unsupported_schema_version")
	}
	if strings.TrimSpace(manifest.BackupID) == "" {
		issues = append(issues, "backup_id_missing")
	}
	if !validTime(manifest.CreatedAt) || !validTime(manifest.ConsistentAt) {
		issues = append(issues, "manifest_timestamp_invalid")
	}
	if manifest.Files == nil {
		issues = append(issues, "manifest_files_missing")
	}
	return &manifest, issues
}

func validateManifestLayout(manifest *Manifest) []string {
	issues := make([]string, 0)
	seen := make(map[string]bool, len(manifest.Files))
	hasDatabase := false
	for _, file := range manifest.Files {
		path, ok := safeRelativePath(file.Path)
		if !ok {
			issues = append(issues, "manifest_unsafe_path")
			continue
		}
		if seen[path] {
			issues = append(issues, "manifest_duplicate_path:"+path)
			continue
		}
		seen[path] = true
		if !allowedManifestPath(path) {
			issues = append(issues, "manifest_path_not_allowed:"+path)
			continue
		}
		if path == DatabaseEntry {
			hasDatabase = true
		}
		size, err := strconv.ParseInt(file.Size, 10, 64)
		if err != nil || size < 0 {
			issues = append(issues, "manifest_size_invalid:"+path)
			continue
		}
		if !hex64Pattern.MatchString(strings.ToLower(file.Sha256)) {
			issues = append(issues, "manifest_digest_invalid:"+path)
		}
	}
	if !hasDatabase {
		issues = append(issues, "manifest_database_missing")
	}
	return issues
}

func allowedManifestPath(path string) bool {
	if path == DatabaseEntry {
		return true
	}
	if strings.HasPrefix(path, UploadsPrefix) {
		return len(strings.Split(path, "/")) >= 3
	}
	if strings.HasPrefix(path, SnapshotsPrefix) {
		parts := strings.Split(path, "/")
		return len(parts) == 4 && parts[2] == assetsDirName && strings.HasSuffix(parts[3], assetBinSuffix)
	}
	return false
}

func compareEntrySets(entries map[string]*zip.File, manifest *Manifest) []string {
	issues := make([]string, 0)
	expected := manifest.fileMap()
	for path := range entries {
		if path == ManifestEntry {
			continue
		}
		if _, ok := expected[path]; !ok {
			issues = append(issues, "unexpected_entry:"+path)
		}
	}
	for path := range expected {
		if _, ok := entries[path]; !ok {
			issues = append(issues, "missing_entry:"+path)
		}
	}
	return issues
}

func extractEntries(ctx context.Context, entries map[string]*zip.File, manifest *Manifest,
	workDir string, limits Limits) (int64, []string, error) {
	issues := make([]string, 0)
	var total int64
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return total, nil, err
		}
		entry := entries[file.Path]
		target, ok := joinWithin(workDir, file.Path)
		if !ok {
			issues = append(issues, "unsafe_extract_path:"+file.Path)
			continue
		}
		expectedSize, _ := strconv.ParseInt(file.Size, 10, 64)
		written, err := extractEntry(entry, target, expectedSize, limits.MaxFileBytes)
		if err != nil {
			issues = append(issues, "entry_extract_failed:"+file.Path)
			continue
		}
		total += written
		if total > limits.MaxExtractedBytes {
			return total, []string{"extracted_size_limit_exceeded"}, nil
		}
		if written != expectedSize {
			issues = append(issues, "entry_size_mismatch:"+file.Path)
			continue
		}
		_, digest, err := verifyRegularFile(target)
		if err != nil || !strings.EqualFold(digest, file.Sha256) {
			issues = append(issues, "entry_digest_mismatch:"+file.Path)
		}
	}
	return total, issues, nil
}

func extractEntry(entry *zip.File, target string, expectedSize, maxFileBytes int64) (int64, error) {
	if entry == nil {
		return 0, errors.New("entry missing")
	}
	if expectedSize > maxFileBytes {
		return 0, limitError("entry exceeds the single-file limit")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return 0, err
	}
	handle, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	reader, err := entry.Open()
	if err != nil {
		_ = handle.Close()
		_ = os.Remove(target)
		return 0, err
	}
	defer reader.Close()
	written, err := io.Copy(handle, io.LimitReader(reader, expectedSize+1))
	if err != nil {
		_ = handle.Close()
		_ = os.Remove(target)
		return 0, err
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		_ = os.Remove(target)
		return 0, err
	}
	if err := handle.Close(); err != nil {
		_ = os.Remove(target)
		return 0, err
	}
	if written > expectedSize {
		_ = os.Remove(target)
		return written, fmt.Errorf("entry larger than declared")
	}
	return written, nil
}

func validateExtractedDatabase(ctx context.Context, path string, manifest *Manifest, limits Limits) ([]string, error) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return []string{"database_missing"}, nil
	}
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(path))
	if err != nil {
		return []string{"database_unreadable"}, nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, limits.ValidateTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return []string{"database_unreadable"}, nil
	}
	if _, err := db.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return []string{"database_unreadable"}, nil
	}

	issues := make([]string, 0)
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil ||
		!strings.EqualFold(integrity, "ok") {
		issues = append(issues, "database_integrity_check_failed")
	}
	switch manifest.SchemaVersion {
	case 1:
		issues = append(issues, validateBusinessSchemaV1(ctx, db)...)
		issues = append(issues, validateBusinessData(ctx, db, manifest.fileMap())...)
		if len(issues) > 0 {
			return issues, nil
		}
		_ = db.Close()
		if err := upgradeSchemaV1(ctx, path); err != nil {
			return []string{"schema_v1_upgrade_failed"}, nil
		}
	case 2:
		issues = append(issues, validateBusinessSchemaV2(ctx, db)...)
		issues = append(issues, validateBusinessData(ctx, db, manifest.fileMap())...)
		issues = append(issues, validateManifestCounts(ctx, db, manifest)...)
		if len(issues) > 0 {
			return issues, nil
		}
		_ = db.Close()
		if err := upgradeSchemaV2(ctx, path); err != nil {
			return []string{"schema_v2_upgrade_failed"}, nil
		}
	case 3:
		issues = append(issues, validateSchemaObjects(ctx, db, schemaV3Tables, nil)...)
		issues = append(issues, validateBusinessData(ctx, db, manifest.fileMap())...)
		issues = append(issues, validateReleaseNumberingData(ctx, db)...)
		issues = append(issues, validateManifestCounts(ctx, db, manifest)...)
		if len(issues) > 0 {
			return issues, nil
		}
		_ = db.Close()
		if err := upgradeSchemaV2(ctx, path); err != nil {
			return []string{"schema_v3_upgrade_failed"}, nil
		}
	default:
		return validateExtractedDatabaseV3(ctx, db, manifest), nil
	}
	return validateExtractedDatabaseV3File(ctx, path, manifest, limits)
}

func validateExtractedDatabaseV3(ctx context.Context, db *sql.DB, manifest *Manifest) []string {
	issues := make([]string, 0)
	issues = append(issues, validateBusinessSchema(ctx, db)...)
	issues = append(issues, validateBusinessData(ctx, db, manifest.fileMap())...)
	issues = append(issues, validateReleaseNumberingData(ctx, db)...)
	issues = append(issues, validateManifestCounts(ctx, db, manifest)...)
	return issues
}

func validateExtractedDatabaseV3File(ctx context.Context, path string, manifest *Manifest, limits Limits) ([]string, error) {
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(path))
	if err != nil {
		return []string{"database_unreadable"}, nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, limits.ValidateTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return []string{"database_unreadable"}, nil
	}
	if _, err := db.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return []string{"database_unreadable"}, nil
	}
	issues := make([]string, 0)
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil ||
		!strings.EqualFold(integrity, "ok") {
		issues = append(issues, "database_integrity_check_failed")
	}
	issues = append(issues, validateExtractedDatabaseV3(ctx, db, manifest)...)
	if len(issues) == 0 {
		manifest.SchemaVersion = SchemaVersion
	}
	return issues, nil
}

func validateManifestCounts(ctx context.Context, db *sql.DB, manifest *Manifest) []string {
	counts, err := collectCountsSQL(ctx, db)
	if err != nil {
		return []string{"database_counts_unreadable"}
	}
	if counts != manifest.Counts {
		return []string{"manifest_counts_mismatch"}
	}
	return nil
}

// upgradeSchemaV1 converts a legacy package database inside the isolated
// workspace. It never touches the live business root.
func upgradeSchemaV1(ctx context.Context, path string) error {
	db, err := openBusinessDB(path)
	if err != nil {
		return err
	}
	defer closeGorm(db)
	if err := configdb.MigrateScopeIdentity(ctx, db); err != nil {
		return err
	}
	return configdb.EnsureReleaseNumbering(ctx, db)
}

// upgradeSchemaV2 adds the release number counter inside the isolated
// workspace and backfills it from all committed version names.
func upgradeSchemaV2(ctx context.Context, path string) error {
	db, err := openBusinessDB(path)
	if err != nil {
		return err
	}
	defer closeGorm(db)
	return configdb.EnsureReleaseNumbering(ctx, db)
}

func readOnlySQLiteDSN(path string) string {
	slash := filepath.ToSlash(path)
	return "file:" + slash + "?mode=ro&_pragma=query_only(1)&_pragma=trusted_schema(0)"
}

// jsonUnmarshalStrict rejects unknown fields in the manifest so a newer
// package cannot be imported by silently ignoring new semantics.
func jsonUnmarshalStrict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
