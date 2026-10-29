package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
)

var (
	assetSchemeRef = regexp.MustCompile(`asset://([a-zA-Z0-9\-]+)`)
	assetURLRef    = regexp.MustCompile(`/api/v1/asset/file/([a-zA-Z0-9\-]+)`)
	hex64Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// validateBusinessSchema accepts only the known business tables, indexes and
// exact column sets. Views, triggers, virtual tables and unknown columns mean
// the package was not produced by this version and must not be restored.
func validateBusinessSchema(ctx context.Context, db *sql.DB) []string {
	issues := validateSchemaObjects(ctx, db, businessTables, nil)
	if sqliteColumnExists(db, "config_release_version", "storage_backend") {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM config_release_version WHERE storage_backend NOT IN ('','local') OR storage_bucket <> '' OR storage_prefix <> '' OR storage_attempt <> ''").Scan(&count); err != nil || count != 0 {
			issues = append(issues, "nonlocal_snapshots_not_supported")
		}
	}
	return issues
}

// validateBusinessSchemaV2 validates the identity-aware package layout that
// predates the release number counter.
func validateBusinessSchemaV2(ctx context.Context, db *sql.DB) []string {
	return validateSchemaObjects(ctx, db, schemaV2Tables, nil)
}

// validateBusinessSchemaV1 validates the pre-identity package layout.
func validateBusinessSchemaV1(ctx context.Context, db *sql.DB) []string {
	return validateSchemaObjects(ctx, db, schemaV1Tables, nil)
}

// validateInstalledSchema also accepts the authentication tables that the
// startup switch creates in the installed root. Uploaded packages never
// contain them.
func validateInstalledSchema(ctx context.Context, db *sql.DB) []string {
	specs := businessTables
	if !sqliteColumnExists(db, "config_release_version", "storage_backend") {
		specs = schemaV3Tables
	}
	if !sqliteColumnExists(db, "config_release_head", "last_version_number") {
		// A committed journal may survive an upgrade from the previous binary.
		// AutoMigrate adds the counter during the normal initialization that
		// follows this validation.
		specs = schemaV2Tables
	}
	return validateSchemaObjects(ctx, db, specs, map[string]bool{
		"auth_users": true, "auth_sessions": true, "config_write_gate": true,
		"release_storage_attempt": true,
	})
}

func sqliteColumnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

func validateSchemaObjects(ctx context.Context, db *sql.DB, specs []tableSpec, extraTables map[string]bool) []string {
	issues := make([]string, 0)
	allowed := allowedTableSetFor(specs)
	found := make(map[string]bool, len(allowed))

	rows, err := db.QueryContext(ctx,
		"SELECT type, name, tbl_name FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'")
	if err != nil {
		return append(issues, "schema_unreadable")
	}
	defer rows.Close()
	for rows.Next() {
		var objectType, name, tableName string
		if err := rows.Scan(&objectType, &name, &tableName); err != nil {
			return append(issues, "schema_unreadable")
		}
		switch objectType {
		case "table":
			if _, ok := allowed[name]; !ok {
				if extraTables != nil && extraTables[name] {
					continue
				}
				issues = append(issues, "unknown_table:"+name)
				continue
			}
			found[name] = true
		case "index":
			if _, ok := allowed[tableName]; !ok {
				if extraTables == nil || !extraTables[tableName] {
					issues = append(issues, "index_on_unknown_table:"+name)
				}
			}
		default:
			issues = append(issues, "unsupported_schema_object:"+objectType+":"+name)
		}
	}
	if err := rows.Err(); err != nil {
		return append(issues, "schema_unreadable")
	}

	for _, spec := range specs {
		if !found[spec.Name] {
			issues = append(issues, "missing_table:"+spec.Name)
			continue
		}
		issues = append(issues, validateColumns(ctx, db, spec)...)
	}
	return issues
}

func validateColumns(ctx context.Context, db *sql.DB, spec tableSpec) []string {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+spec.Name+")")
	if err != nil {
		return []string{"table_info_unreadable:" + spec.Name}
	}
	defer rows.Close()

	actual := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return []string{"table_info_unreadable:" + spec.Name}
		}
		actual[name] = true
	}
	if err := rows.Err(); err != nil {
		return []string{"table_info_unreadable:" + spec.Name}
	}

	issues := make([]string, 0)
	expected := make(map[string]bool, len(spec.Columns))
	for _, column := range spec.Columns {
		expected[column] = true
		if !actual[column] {
			issues = append(issues, "missing_column:"+spec.Name+"."+column)
		}
	}
	for column := range actual {
		if !expected[column] {
			issues = append(issues, "unknown_column:"+spec.Name+"."+column)
		}
	}
	return issues
}

// validateBusinessData checks cross-table relationships that the schema alone
// cannot express, plus payload digests and resource manifest consistency.
func validateBusinessData(ctx context.Context, db *sql.DB, files map[string]FileEntry) []string {
	issues := make([]string, 0)
	issues = append(issues, countIssues(ctx, db, "config_without_environment",
		`SELECT COUNT(*) FROM resource_config c
		 LEFT JOIN environment e ON e.environment_key = c.environment_key
		 WHERE e.environment_key IS NULL`)...)
	issues = append(issues, countIssues(ctx, db, "pipeline_without_environment",
		`SELECT COUNT(*) FROM pipeline p
		 LEFT JOIN environment e ON e.environment_key = p.environment_key
		 WHERE e.environment_key IS NULL`)...)
	issues = append(issues, countIssues(ctx, db, "head_points_to_missing_or_deleted_version",
		`SELECT COUNT(*) FROM config_release_head h
		 LEFT JOIN config_release_version v ON v.snapshot_id = h.active_snapshot_id
		 WHERE h.active_snapshot_id <> ''
		   AND (v.id IS NULL OR v.deleted_at IS NOT NULL
		        OR v.environment_key <> h.environment_key
		        OR v.pipeline_key <> h.pipeline_key)`)...)
	issues = append(issues, countIssues(ctx, db, "release_asset_without_version",
		`SELECT COUNT(*) FROM config_release_asset a
		 LEFT JOIN config_release_version v ON v.snapshot_id = a.snapshot_id
		 WHERE v.id IS NULL`)...)
	issues = append(issues, countIssues(ctx, db, "release_asset_count_mismatch",
		`SELECT COUNT(*) FROM config_release_version v
		 WHERE v.asset_count <> (
		   SELECT COUNT(*) FROM config_release_asset a WHERE a.snapshot_id = v.snapshot_id
		 )`)...)
	issues = append(issues, validatePayloadDigests(ctx, db)...)
	issues = append(issues, validateReleaseManifestDigests(ctx, db)...)
	issues = append(issues, validateAssetFiles(ctx, db, files)...)
	issues = append(issues, validateReleaseFiles(ctx, db, files)...)
	issues = append(issues, validateConfigAssetReferences(ctx, db)...)
	return issues
}

// validateReleaseNumberingData checks the schema-3 invariant: a non-negative
// counter that is at least as large as every canonical version name in the
// same scope, including deleted versions.
func validateReleaseNumberingData(ctx context.Context, db *sql.DB) []string {
	issues := make([]string, 0)
	issues = append(issues, countIssues(ctx, db, "release_number_negative",
		"SELECT COUNT(*) FROM config_release_head WHERE last_version_number < 0")...)
	counterRows, err := db.QueryContext(ctx,
		"SELECT scope_id, last_version_number FROM config_release_head")
	if err != nil {
		return append(issues, "release_numbering_unreadable")
	}
	last := make(map[string]int64)
	for counterRows.Next() {
		var scopeID string
		var value int64
		if err := counterRows.Scan(&scopeID, &value); err != nil {
			_ = counterRows.Close()
			return append(issues, "release_numbering_unreadable")
		}
		last[scopeID] = value
	}
	if err := counterRows.Close(); err != nil {
		return append(issues, "release_numbering_unreadable")
	}
	versionRows, err := db.QueryContext(ctx,
		"SELECT scope_id, version FROM config_release_version")
	if err != nil {
		return append(issues, "release_numbering_unreadable")
	}
	defer versionRows.Close()
	for versionRows.Next() {
		var scopeID, version string
		if err := versionRows.Scan(&scopeID, &version); err != nil {
			return append(issues, "release_numbering_unreadable")
		}
		value, canonical, err := configdb.ParseReleaseSequenceVersion(version)
		if err != nil {
			issues = append(issues, "release_number_overflow:"+scopeID+":"+version)
			continue
		}
		if canonical && value > last[scopeID] {
			issues = append(issues, "release_number_below_history:"+scopeID)
			last[scopeID] = value
		}
	}
	if err := versionRows.Err(); err != nil {
		return append(issues, "release_numbering_unreadable")
	}
	return issues
}

func countIssues(ctx context.Context, db *sql.DB, name, query string) []string {
	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return []string{name + "_unreadable"}
	}
	if count > 0 {
		return []string{fmt.Sprintf("%s:%d", name, count)}
	}
	return nil
}

func validatePayloadDigests(ctx context.Context, db *sql.DB) []string {
	rows, err := db.QueryContext(ctx,
		"SELECT snapshot_id, payload, payload_sha256, manifest_sha256 FROM config_release_version")
	if err != nil {
		return []string{"release_payload_unreadable"}
	}
	defer rows.Close()
	issues := make([]string, 0)
	for rows.Next() {
		var snapshotID, payload, payloadSha, manifestSha string
		if err := rows.Scan(&snapshotID, &payload, &payloadSha, &manifestSha); err != nil {
			return append(issues, "release_payload_unreadable")
		}
		digest := sha256.Sum256([]byte(payload))
		if !hex64Pattern.MatchString(payloadSha) || hex.EncodeToString(digest[:]) != payloadSha {
			issues = append(issues, "payload_digest_mismatch:"+snapshotID)
		}
		if !hex64Pattern.MatchString(manifestSha) {
			issues = append(issues, "manifest_digest_invalid:"+snapshotID)
		}
	}
	if err := rows.Err(); err != nil {
		return append(issues, "release_payload_unreadable")
	}
	return issues
}

// validateReleaseManifestDigests recomputes the release manifest digest with
// the same field order and sorting used by the publishing module.
func validateReleaseManifestDigests(ctx context.Context, db *sql.DB) []string {
	type manifestAsset struct {
		assetID      string
		sourceFileID string
		fileName     string
		contentType  string
		size         int64
		sha256       string
	}
	bySnapshot := make(map[string][]manifestAsset)
	rows, err := db.QueryContext(ctx,
		`SELECT snapshot_id, asset_id, source_file_id, file_name, content_type, size, sha256
		 FROM config_release_asset`)
	if err != nil {
		return []string{"release_asset_table_unreadable"}
	}
	for rows.Next() {
		var snapshotID string
		var asset manifestAsset
		if err := rows.Scan(&snapshotID, &asset.assetID, &asset.sourceFileID, &asset.fileName,
			&asset.contentType, &asset.size, &asset.sha256); err != nil {
			_ = rows.Close()
			return []string{"release_asset_table_unreadable"}
		}
		bySnapshot[snapshotID] = append(bySnapshot[snapshotID], asset)
	}
	if err := rows.Close(); err != nil {
		return []string{"release_asset_table_unreadable"}
	}

	versionRows, err := db.QueryContext(ctx,
		"SELECT snapshot_id, manifest_sha256 FROM config_release_version")
	if err != nil {
		return []string{"release_version_table_unreadable"}
	}
	defer versionRows.Close()
	issues := make([]string, 0)
	for versionRows.Next() {
		var snapshotID, expected string
		if err := versionRows.Scan(&snapshotID, &expected); err != nil {
			return append(issues, "release_version_table_unreadable")
		}
		assets := bySnapshot[snapshotID]
		sort.Slice(assets, func(i, j int) bool { return assets[i].assetID < assets[j].assetID })
		hasher := sha256.New()
		for _, asset := range assets {
			fmt.Fprintf(hasher, "%s\t%s\t%s\t%s\t%d\t%s\n",
				asset.assetID, asset.sourceFileID, asset.fileName, asset.contentType,
				asset.size, asset.sha256)
		}
		if !hex64Pattern.MatchString(strings.ToLower(expected)) ||
			hex.EncodeToString(hasher.Sum(nil)) != strings.ToLower(expected) {
			issues = append(issues, "manifest_digest_mismatch:"+snapshotID)
		}
	}
	if err := versionRows.Err(); err != nil {
		return append(issues, "release_version_table_unreadable")
	}
	return issues
}

func validateAssetFiles(ctx context.Context, db *sql.DB, files map[string]FileEntry) []string {
	rows, err := db.QueryContext(ctx, "SELECT file_id, path, file_size FROM asset")
	if err != nil {
		return []string{"asset_table_unreadable"}
	}
	defer rows.Close()
	issues := make([]string, 0)
	for rows.Next() {
		var fileID, path string
		var size int64
		if err := rows.Scan(&fileID, &path, &size); err != nil {
			return append(issues, "asset_table_unreadable")
		}
		entry, ok := files[normalizeStoredRelative(path)]
		if !ok {
			issues = append(issues, "asset_file_missing:"+fileID)
			continue
		}
		if size > 0 && entry.Size != decimalString(size) {
			issues = append(issues, "asset_file_size_mismatch:"+fileID)
		}
	}
	if err := rows.Err(); err != nil {
		return append(issues, "asset_table_unreadable")
	}
	return issues
}

func validateReleaseFiles(ctx context.Context, db *sql.DB, files map[string]FileEntry) []string {
	rows, err := db.QueryContext(ctx,
		`SELECT a.snapshot_id, a.asset_id, a.size, a.sha256
		 FROM config_release_asset a
		 JOIN config_release_version v ON v.snapshot_id = a.snapshot_id`)
	if err != nil {
		return []string{"release_asset_table_unreadable"}
	}
	defer rows.Close()
	issues := make([]string, 0)
	for rows.Next() {
		var snapshotID, assetID, sha string
		var size int64
		if err := rows.Scan(&snapshotID, &assetID, &size, &sha); err != nil {
			return append(issues, "release_asset_table_unreadable")
		}
		path := releaseArchivePath(snapshotID, assetID)
		entry, ok := files[path]
		if !ok {
			issues = append(issues, "release_asset_file_missing:"+snapshotID+"/"+assetID)
			continue
		}
		if entry.Size != decimalString(size) || entry.Sha256 != sha {
			issues = append(issues, "release_asset_file_mismatch:"+snapshotID+"/"+assetID)
		}
	}
	if err := rows.Err(); err != nil {
		return append(issues, "release_asset_table_unreadable")
	}
	return issues
}

func validateConfigAssetReferences(ctx context.Context, db *sql.DB) []string {
	assetRows, err := db.QueryContext(ctx, "SELECT file_id FROM asset")
	if err != nil {
		return []string{"asset_table_unreadable"}
	}
	known := make(map[string]bool)
	for assetRows.Next() {
		var fileID string
		if err := assetRows.Scan(&fileID); err != nil {
			_ = assetRows.Close()
			return []string{"asset_table_unreadable"}
		}
		known[fileID] = true
	}
	if err := assetRows.Close(); err != nil {
		return []string{"asset_table_unreadable"}
	}

	rows, err := db.QueryContext(ctx, "SELECT resource_key, content FROM resource_config")
	if err != nil {
		return []string{"config_table_unreadable"}
	}
	defer rows.Close()
	issues := make([]string, 0)
	for rows.Next() {
		var resourceKey, content string
		if err := rows.Scan(&resourceKey, &content); err != nil {
			return append(issues, "config_table_unreadable")
		}
		for _, id := range referencedAssetIDs(content) {
			if !known[id] {
				issues = append(issues, "config_reference_missing_asset:"+resourceKey+":"+id)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return append(issues, "config_table_unreadable")
	}
	return issues
}

func referencedAssetIDs(content string) []string {
	seen := make(map[string]bool)
	ids := make([]string, 0)
	for _, match := range assetSchemeRef.FindAllStringSubmatch(content, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			ids = append(ids, match[1])
		}
	}
	for _, match := range assetURLRef.FindAllStringSubmatch(content, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			ids = append(ids, match[1])
		}
	}
	return ids
}

// externalReferenceCount counts unmanaged external URLs mentioned in configs;
// these are preserved as strings but their remote content is never fetched.
func externalReferenceCount(ctx context.Context, db *sql.DB) int {
	rows, err := db.QueryContext(ctx, "SELECT content FROM resource_config")
	if err != nil {
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return count
		}
		lower := strings.ToLower(content)
		if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
			count++
		}
	}
	return count
}

func decimalString(value int64) string {
	return fmt.Sprintf("%d", value)
}
