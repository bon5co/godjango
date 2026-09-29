package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bon5co/godjango/database"
)

func TestSQLiteDefaultAndForeignKeysAcrossConnectionReplacement(t *testing.T) {
	ctx := context.Background()
	config := database.DefaultSQLiteConfig("sqlite:" + filepath.Join(t.TempDir(), "test.sqlite"))
	if config.MaxOpenConns != 1 {
		t.Fatalf("SQLite MaxOpenConns = %d", config.MaxOpenConns)
	}
	config.ConnMaxLifetime = time.Millisecond
	db, err := database.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Dialect() != "sqlite" || db.Pool() != nil {
		t.Fatalf("dialect=%q pool=%v", db.Dialect(), db.Pool())
	}
	for pragma, want := range map[string]string{"foreign_keys": "1", "journal_mode": "wal", "busy_timeout": "5000"} {
		var got string
		if err := db.Bun().NewRaw("PRAGMA "+pragma).Scan(ctx, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%q, want %q", pragma, got, want)
		}
	}
	if _, err := db.Bun().ExecContext(ctx, "CREATE TABLE parent (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().ExecContext(ctx, "CREATE TABLE child (parent_id integer REFERENCES parent(id))"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	for pragma, want := range map[string]string{"foreign_keys": "1", "journal_mode": "wal", "busy_timeout": "5000"} {
		var got string
		if err := db.Bun().NewRaw("PRAGMA "+pragma).Scan(ctx, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("after replacement %s=%q, want %q", pragma, got, want)
		}
	}
	if _, err := db.Bun().ExecContext(ctx, "INSERT INTO child (parent_id) VALUES (42)"); err == nil {
		t.Fatal("foreign key enforcement disabled on replacement connection")
	}
}

func TestDriverFieldSelectsBackend(t *testing.T) {
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "test.sqlite")
	if config := database.DefaultConfig(dsn); config.Driver != "postgres" {
		t.Fatalf("DefaultConfig.Driver = %q", config.Driver)
	}
	if config := database.DefaultSQLiteConfig(dsn); config.Driver != "sqlite" {
		t.Fatalf("DefaultSQLiteConfig.Driver = %q", config.Driver)
	}
	invalid := database.DefaultConfig(dsn)
	invalid.Driver = "unknown"
	if _, err := database.Open(context.Background(), invalid); err == nil {
		t.Fatal("Open accepted unsupported Driver")
	}
	postgres := database.DefaultConfig(dsn)
	postgres.PingTimeout = 10 * time.Millisecond
	if _, err := database.Open(context.Background(), postgres); err == nil {
		t.Fatal("Open selected SQLite from DSN despite Driver=postgres")
	}
}
