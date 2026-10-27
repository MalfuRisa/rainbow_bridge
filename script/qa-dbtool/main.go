// Command qa-dbtool is a read-only SQLite inspector for the isolated local QA suite.
// It never writes to the database; used to compare API results against committed rows.
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	_ "github.com/glebarez/go-sqlite"
)

const usage = `usage:
  qa-dbtool dump <dbPath> <table> [column=value ...]
  qa-dbtool hash <filePath>
  qa-dbtool tables <dbPath>
`

// Tables allowed for inspection, mapped to the primary key used for stable ordering.
var knownTables = map[string]string{
	"environment":     "environment_key",
	"pipeline":        "pipeline_key",
	"resource_config": "id",
	"asset":           "file_id",
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "qa-dbtool:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "dump":
		return runDump(args[1:])
	case "hash":
		return runHash(args[1:])
	case "tables":
		return runTables(args[1:])
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func openDB(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("db path required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func runDump(args []string) error {
	if len(args) < 2 {
		return errors.New(usage)
	}
	dbPath, table := args[0], args[1]
	orderBy, ok := knownTables[table]
	if !ok {
		allowed := make([]string, 0, len(knownTables))
		for name := range knownTables {
			allowed = append(allowed, name)
		}
		sort.Strings(allowed)
		return fmt.Errorf("table %q not allowed (allowed: %s)", table, strings.Join(allowed, ", "))
	}
	db, err := openDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	columns, err := tableColumns(db, table)
	if err != nil {
		return err
	}
	query := fmt.Sprintf("SELECT * FROM %s", table)
	var values []any
	conditions := make([]string, 0, len(args)-2)
	for _, filter := range args[2:] {
		column, value, found := strings.Cut(filter, "=")
		if !found {
			return fmt.Errorf("filter %q must be column=value", filter)
		}
		if !columns[column] {
			return fmt.Errorf("column %q not found in %s", column, table)
		}
		conditions = append(conditions, fmt.Sprintf("%s = ?", column))
		values = append(values, value)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if columns[orderBy] {
		query += fmt.Sprintf(" ORDER BY %s", orderBy)
	}

	rows, err := db.Query(query, values...)
	if err != nil {
		return err
	}
	defer rows.Close()

	records, err := collectRows(rows)
	if err != nil {
		return err
	}
	return writeJSON(map[string]any{"table": table, "count": len(records), "rows": records})
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func collectRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0)
	for rows.Next() {
		cells := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range cells {
			pointers[i] = &cells[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		record := make(map[string]any, len(cols))
		for i, col := range cols {
			record[col] = normalizeCell(cells[i])
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func normalizeCell(cell any) any {
	switch value := cell.(type) {
	case []byte:
		return string(value)
	default:
		return cell
	}
}

func runHash(args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	path := args[0]
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return err
	}
	return writeJSON(map[string]any{
		"path":   path,
		"size":   size,
		"sha256": fmt.Sprintf("%x", hasher.Sum(nil)),
	})
}

func runTables(args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	db, err := openDB(args[0])
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		return err
	}
	defer rows.Close()
	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return writeJSON(map[string]any{"tables": names})
}

func writeJSON(payload any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(payload)
}
