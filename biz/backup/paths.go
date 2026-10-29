package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// Layout contains the resolved, absolute storage roots used by backup,
// restore and the offline tool. None of these values is returned to clients.
type Layout struct {
	BusinessRoot string
	ControlDir   string
	DatabasePath string
	DatabaseRel  string
	UploadsDir   string
	ReleaseDir   string
	ReleaseRel   string
}

// ResolveLayout validates the deployment shape required by the first version:
// one local SQLite file, local uploads and release snapshots all inside the
// replaceable <cwd>/data root, Redis disabled, and a control directory on the
// same local volume but outside the business root.
func ResolveLayout(cfg *config.Config) (*Layout, []string) {
	reasons := unsupportedReasons(cfg)
	if len(reasons) > 0 {
		return nil, reasons
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, []string{"business_root_unavailable"}
	}
	root := filepath.Join(cwd, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, []string{"business_root_unavailable"}
	}
	root, err = resolvePath(root)
	if err != nil {
		return nil, []string{"business_root_unavailable"}
	}

	dbPath, ok := resolveDatabasePath(cwd, cfg.Database.SQLite.Path)
	if !ok || !isWithin(root, dbPath) || samePath(root, dbPath) {
		return nil, append(reasons, "database_outside_business_root")
	}

	releaseRoot, ok := resolveReleaseRoot(root)
	if !ok || !isWithin(root, releaseRoot) {
		return nil, append(reasons, "release_snapshots_outside_business_root")
	}

	control, reason := resolveControlDir(cfg.Backup.ControlDir, root, cwd)
	if reason != "" {
		return nil, append(reasons, reason)
	}
	if !sameVolume(control, root) {
		return nil, append(reasons, "backup_control_dir_different_volume")
	}

	rel, err := filepath.Rel(root, dbPath)
	if err != nil {
		return nil, append(reasons, "database_outside_business_root")
	}
	releaseRel, err := filepath.Rel(root, releaseRoot)
	if err != nil {
		return nil, append(reasons, "release_snapshots_outside_business_root")
	}
	return &Layout{
		BusinessRoot: root,
		ControlDir:   control,
		DatabasePath: dbPath,
		DatabaseRel:  filepath.ToSlash(rel),
		UploadsDir:   filepath.Join(root, "uploads"),
		ReleaseDir:   releaseRoot,
		ReleaseRel:   filepath.ToSlash(releaseRel),
	}, reasons
}

func unsupportedReasons(cfg *config.Config) []string {
	reasons := make([]string, 0, 4)
	driver := strings.ToLower(strings.TrimSpace(cfg.Database.Driver))
	if driver != "sqlite" && driver != "sqlite3" {
		reasons = append(reasons, "database_driver_not_supported")
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Storage.Type), "local") {
		reasons = append(reasons, "storage_not_local")
	}
	if cfg.ReleaseStorage.Type != "" && cfg.ReleaseStorage.Type != "local" {
		reasons = append(reasons, "snapshot_storage_not_local")
	}
	if driver == "sqlite" || driver == "sqlite3" {
		if reason := snapshotBackupInventory(cfg.Database.SQLite.Path); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	if cfg.Redis.Enabled {
		reasons = append(reasons, "redis_enabled")
	}
	if !cfg.Backup.Enabled {
		reasons = append(reasons, "backup_disabled")
	}
	return reasons
}

func resolveDatabasePath(cwd, configured string) (string, bool) {
	value := strings.TrimSpace(configured)
	if value == "" {
		return "", false
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(cwd, value)
	}
	resolved, err := resolvePath(value)
	return resolved, err == nil
}

func resolveReleaseRoot(businessRoot string) (string, bool) {
	value := strings.TrimSpace(os.Getenv("RAINBOW_RELEASE_DIR"))
	if value == "" {
		return filepath.Join(businessRoot, "release-snapshots"), true
	}
	if !filepath.IsAbs(value) {
		return "", false
	}
	resolved, err := resolvePath(value)
	return resolved, err == nil
}

func resolveControlDir(configured, businessRoot, cwd string) (string, string) {
	value := strings.TrimSpace(configured)
	if value == "" {
		return "", "backup_control_dir_missing"
	}
	if !filepath.IsAbs(value) {
		return "", "backup_control_dir_not_absolute"
	}
	if err := os.MkdirAll(value, 0o700); err != nil {
		return "", "backup_control_dir_unavailable"
	}
	control, err := resolvePath(value)
	if err != nil {
		return "", "backup_control_dir_unavailable"
	}
	switch {
	case samePath(control, businessRoot), isWithin(businessRoot, control):
		return "", "backup_control_dir_inside_business_root"
	case isWithin(control, businessRoot):
		return "", "backup_control_dir_contains_business_root"
	case samePath(control, cwd), isWithin(control, cwd):
		return "", "backup_control_dir_contains_program"
	}
	return control, ""
}

// resolvePath resolves symlinks while still accepting paths whose final
// component does not exist yet.
func resolvePath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute))
	if parentErr != nil {
		return "", parentErr
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func isWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if relative == "." {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// safeRelativePath validates a path taken from a manifest or a database row.
// It rejects absolute paths, drive letters, UNC prefixes, separators other
// than '/', NUL bytes and any traversal segment.
func safeRelativePath(value string) (string, bool) {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, "\\") {
		return "", false
	}
	if strings.HasPrefix(value, "/") || filepath.VolumeName(value) != "" {
		return "", false
	}
	// Reject traversal segments before cleaning so "uploads/../x" cannot be
	// quietly normalized into an accepted path.
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// joinWithin resolves a validated relative path below root and makes sure the
// result cannot escape through an unexpected separator.
func joinWithin(root, relative string) (string, bool) {
	cleaned, ok := safeRelativePath(relative)
	if !ok {
		return "", false
	}
	target := filepath.Join(root, filepath.FromSlash(cleaned))
	if !isWithin(root, target) {
		return "", false
	}
	return target, true
}
