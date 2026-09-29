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
	config := database.DefaultConfig("sqlite:" + filepath.Join(t.TempDir(), "test.sqlite"))
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
	if _, err := db.Bun().ExecContext(ctx, "CREATE TABLE parent (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().ExecContext(ctx, "CREATE TABLE child (parent_id integer REFERENCES parent(id))"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if _, err := db.Bun().ExecContext(ctx, "INSERT INTO child (parent_id) VALUES (42)"); err == nil {
		t.Fatal("foreign key enforcement disabled on replacement connection")
	}
}
