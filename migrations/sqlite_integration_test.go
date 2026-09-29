//go:build sqlite_integration

package migrations_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/migrations"
	"github.com/bon5co/godjango/project"
)

func TestSQLitePerFileMigrationOverridesUpAndFallsBackDown(t *testing.T) {
	files := fstest.MapFS{
		"20260731120001_books.tx.up.sql":     {Data: []byte("CREATE TABLE books (flavor text NOT NULL DEFAULT 'shared');")},
		"20260731120001_books.sqlite.up.sql": {Data: []byte("CREATE TABLE books (flavor text NOT NULL DEFAULT 'sqlite');")},
		"20260731120001_books.tx.down.sql":   {Data: []byte("DROP TABLE books;")},
	}
	configured, err := project.New(fixtureSettings{}, fixtureApp{name: "books", migrations: files})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := migrations.CollectForDialect(configured, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.Open(ctx, database.DefaultSQLiteConfig("sqlite:"+filepath.Join(t.TempDir(), "test.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runner, err := migrations.NewRunner(db, catalog, migrations.DefaultRunnerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().ExecContext(ctx, "INSERT INTO books DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	var flavor string
	if err := db.Bun().NewRaw("SELECT flavor FROM books").Scan(ctx, &flavor); err != nil {
		t.Fatal(err)
	}
	if flavor != "sqlite" {
		t.Fatalf("flavor=%q, want sqlite", flavor)
	}
	if _, err := runner.Rollback(ctx, migrations.ConfirmRollback); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().ExecContext(ctx, "SELECT * FROM books"); err == nil {
		t.Fatal("fallback down migration did not drop table")
	}
}
