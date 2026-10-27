// Read-only SQLite preflight. This command does not run application startup or migrations.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/pkg/database"
	"gorm.io/gorm"
)

func main() {
	file := flag.String("sqlite", "", "existing SQLite database to inspect read-only")
	flag.Parse()
	if *file == "" {
		fmt.Fprintln(os.Stderr, "--sqlite <existing database> is required")
		os.Exit(1)
	}
	absolute, err := filepath.Abs(*file)
	if err != nil {
		fail(err)
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	db, err := gorm.Open(sqlite.Open(uri.String()), &gorm.Config{})
	if err != nil {
		fail(err)
	}
	raw, err := db.DB()
	if err != nil {
		fail(err)
	}
	defer raw.Close()
	conflicts, err := database.CheckConfigAliases(context.Background(), db)
	if err != nil {
		fail(err)
	}
	if conflicts == nil {
		conflicts = []database.AliasConflict{}
	}
	if err := json.NewEncoder(os.Stdout).Encode(conflicts); err != nil {
		fail(err)
	}
	if len(conflicts) > 0 {
		os.Exit(2)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
