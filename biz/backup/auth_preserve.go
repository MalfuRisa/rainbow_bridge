package backup

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

var authUserColumns = []string{
	"id", "username", "password_hash", "role", "disabled",
	"credential_version", "created_at", "updated_at",
}

// readAuthRows reads only the target instance's administrator rows. The
// downloaded backup never contains this table; it is copied locally during the
// startup switch.
func readAuthRows(root, databaseRel string) ([][]any, error) {
	databasePath, ok := joinWithin(root, databaseRel)
	if !ok {
		return nil, fmt.Errorf("invalid database path")
	}
	if _, err := os.Stat(databasePath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(databasePath))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var table string
	if err := db.QueryRow(
		"SELECT name FROM sqlite_schema WHERE type = 'table' AND name = 'auth_users'").Scan(&table); err != nil {
		if isNoSuchTable(err) {
			return nil, nil
		}
		return nil, err
	}
	query := "SELECT " + strings.Join(authUserColumns, ", ") + " FROM auth_users ORDER BY id"
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(authUserColumns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		records = append(records, values)
	}
	return records, rows.Err()
}

// applyAuthRows creates the authentication tables in the restored root with
// the target administrator credentials and zero sessions.
func applyAuthRows(root, databaseRel string, records [][]any) error {
	databasePath, ok := joinWithin(root, databaseRel)
	if !ok {
		return fmt.Errorf("invalid database path")
	}
	db, err := openSQLite(databasePath)
	if err != nil {
		return err
	}
	defer closeGorm(db)
	if err := db.AutoMigrate(&model.AuthUser{}, &model.AuthSession{}); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM auth_sessions").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM auth_users").Error; err != nil {
			return err
		}
		placeholders := make([]string, len(authUserColumns))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		insert := fmt.Sprintf("INSERT INTO auth_users (%s) VALUES (%s)",
			strings.Join(authUserColumns, ", "), strings.Join(placeholders, ", "))
		for _, values := range records {
			if len(values) != len(authUserColumns) {
				return fmt.Errorf("invalid administrator record")
			}
			if err := tx.Exec(insert, values...).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func isNoSuchTable(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no such table")
}
