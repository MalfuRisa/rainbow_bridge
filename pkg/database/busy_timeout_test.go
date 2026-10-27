package database

import (
	"path/filepath"
	"testing"

	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// 写协调依赖 SQLite 的有界等待语义:通过 database.Open 打开的连接必须带 busy_timeout。
func TestSQLiteOpenAppliesBusyTimeout(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy.db")
	conn, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: dbPath},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	var timeout int
	if err := conn.Raw("PRAGMA busy_timeout").Scan(&timeout).Error; err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if timeout != 10000 {
		t.Fatalf("expected busy_timeout=10000, got %d", timeout)
	}
}

func TestWithSQLiteBusyTimeoutDSNForms(t *testing.T) {
	cases := map[string]string{
		"data/resource.db":                       "data/resource.db?_pragma=busy_timeout(10000)",
		"data/resource.db?cache=shared":          "data/resource.db?cache=shared&_pragma=busy_timeout(10000)",
		"file:mem.db?_pragma=busy_timeout(5000)": "file:mem.db?_pragma=busy_timeout(5000)",
	}
	for input, expected := range cases {
		if got := withSQLiteBusyTimeout(input); got != expected {
			t.Fatalf("withSQLiteBusyTimeout(%q) = %q, want %q", input, got, expected)
		}
	}
}
