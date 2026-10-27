package db

import (
	"errors"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConfigConflictClassification(t *testing.T) {
	cases := []struct {
		err   error
		alias bool
	}{
		{&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'a' for key 'resource_config.uk_config_active_alias'"}, true},
		{&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'a' for key 'uk_config_resource'"}, false},
		{&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'a' for key 'uk_config_active_alias_other'"}, false},
		{&mysql.MySQLError{Number: 1205, Message: "Lock timeout"}, false},
		{&pgconn.PgError{Code: "23505", ConstraintName: "uk_config_active_alias"}, true},
		{&pgconn.PgError{Code: "23505", ConstraintName: "uk_config_resource"}, false},
		{&pgconn.PgError{Code: "55P03"}, false},
	}
	for _, tc := range cases {
		result := configWriteError(tc.err)
		if errors.Is(result, ErrConfigAliasExists) != tc.alias {
			t.Fatalf("err=%v result=%v", tc.err, result)
		}
		if !tc.alias && result != tc.err {
			t.Fatal("unrelated failure swallowed")
		}
	}
}
