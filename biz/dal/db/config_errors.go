package db

import (
	"errors"
	"regexp"
	"strings"

	sqlite "github.com/glebarez/go-sqlite"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrConfigAliasExists = errors.New("该环境和渠道下已存在相同别名的配置")
var mysqlAliasKey = regexp.MustCompile("for key ['`](?:[^'`]+\\.)?uk_config_active_alias['`]")

func configWriteError(err error) error {
	if err == nil {
		return nil
	}
	var sq *sqlite.Error
	if errors.As(err, &sq) && sq.Code() == 2067 && strings.Contains(sq.Error(), "resource_config.environment_key, resource_config.pipeline_key, resource_config.alias") {
		return ErrConfigAliasExists
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "uk_config_active_alias" {
		return ErrConfigAliasExists
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) && my.Number == 1062 && mysqlAliasKey.MatchString(my.Message) {
		return ErrConfigAliasExists
	}
	return err
}
