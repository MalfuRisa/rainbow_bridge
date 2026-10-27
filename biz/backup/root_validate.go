package backup

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// scanRootFiles hashes every managed file under a business root and maps it to
// the logical package path. Snapshot files under a custom release directory
// are still keyed as release-snapshots/... for validation.
func scanRootFiles(root, releaseRel string, limits Limits) (map[string]FileEntry, error) {
	files := make(map[string]FileEntry)
	var total int64
	count := 0
	add := func(path, logical string) error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("file exceeds limit")
		}
		size, digest, err := verifyRegularFile(path)
		if err != nil {
			return err
		}
		count++
		if count > limits.MaxFiles {
			return fmt.Errorf("too many files")
		}
		total += size
		if total > limits.MaxExtractedBytes {
			return fmt.Errorf("files exceed total size limit")
		}
		files[logical] = FileEntry{Path: logical, Size: decimalString(size), Sha256: digest}
		return nil
	}
	walk := func(rootPath, logicalPrefix string) error {
		return filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if entry.IsDir() {
				if entry.Name() == ".staging" {
					return filepath.SkipDir
				}
				return nil
			}
			relative, err := filepath.Rel(rootPath, path)
			if err != nil {
				return err
			}
			return add(path, logicalPrefix+filepath.ToSlash(relative))
		})
	}
	if err := walk(filepath.Join(root, "uploads"), UploadsPrefix); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	releaseRoot := filepath.Join(root, filepath.FromSlash(releaseRel))
	if err := walk(releaseRoot, SnapshotsPrefix); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return files, nil
}

// validateRootBusiness performs a startup/commit validation of an actual
// business root. authAllowed permits the authentication tables created by the
// startup switch; package validation never permits them.
func validateRootBusiness(ctx context.Context, root, databaseRel, releaseRel string,
	limits Limits, authAllowed bool) []string {
	databasePath, ok := joinWithin(root, databaseRel)
	if !ok {
		return []string{"database_path_invalid"}
	}
	if info, err := os.Stat(databasePath); err != nil || !info.Mode().IsRegular() {
		return []string{"database_missing"}
	}
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(databasePath))
	if err != nil {
		return []string{"database_unreadable"}
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return []string{"database_unreadable"}
	}
	if _, err := db.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return []string{"database_unreadable"}
	}
	issues := make([]string, 0)
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil ||
		!strings.EqualFold(integrity, "ok") {
		issues = append(issues, "database_integrity_check_failed")
	}
	if authAllowed {
		issues = append(issues, validateInstalledSchema(ctx, db)...)
	} else {
		issues = append(issues, validateBusinessSchema(ctx, db)...)
	}
	if sqliteColumnExists(db, "config_release_head", "last_version_number") {
		issues = append(issues, validateReleaseNumberingData(ctx, db)...)
	}
	files, err := scanRootFiles(root, releaseRel, limits)
	if err != nil {
		issues = append(issues, "resource_files_unreadable")
	} else {
		issues = append(issues, validateBusinessData(ctx, db, files)...)
	}
	return issues
}

func databaseHasAuthTables(root, databaseRel string) bool {
	databasePath, ok := joinWithin(root, databaseRel)
	if !ok {
		return false
	}
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(databasePath))
	if err != nil {
		return false
	}
	defer db.Close()
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_schema WHERE type = 'table' AND name = 'auth_users'").Scan(&name)
	return err == nil && name != ""
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
