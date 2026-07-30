package migrations_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/bon5co/godjango/migrations"
	"github.com/bon5co/godjango/project"
)

type fixtureSettings struct{}

func (fixtureSettings) Validate() error { return nil }

type fixtureApp struct {
	name       string
	migrations fs.FS
}

func (app fixtureApp) Name() string       { return app.name }
func (app fixtureApp) MigrationFS() fs.FS { return app.migrations }

type plainApp struct{ name string }

func (app plainApp) Name() string { return app.name }

func migrationFiles(id, name string) fstest.MapFS {
	return fstest.MapFS{
		id + "_" + name + ".tx.up.sql":   {Data: []byte("SELECT 1;\n")},
		id + "_" + name + ".tx.down.sql": {Data: []byte("SELECT 1;\n")},
	}
}

func TestCatalogCollectsExplicitAppsInMigrationOrder(t *testing.T) {
	configured, err := project.New(
		fixtureSettings{},
		fixtureApp{
			name:       "accounts",
			migrations: migrationFiles("20260731120002", "accounts"),
		},
		fixtureApp{
			name:       "books",
			migrations: migrationFiles("20260731120001", "books"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	catalog, err := migrations.Collect(configured)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	names := catalog.Names()
	if len(names) != 2 ||
		names[0] != "20260731120001_books" ||
		names[1] != "20260731120002_accounts" {
		t.Fatalf("Names() = %v", names)
	}
	names[0] = "mutated"
	if catalog.Names()[0] != "20260731120001_books" {
		t.Error("Names() exposed catalog mutation")
	}
}

func TestCatalogRejectsDuplicateGlobalIdentity(t *testing.T) {
	configured, err := project.New(
		fixtureSettings{},
		fixtureApp{
			name:       "accounts",
			migrations: migrationFiles("20260731120001", "accounts"),
		},
		fixtureApp{
			name:       "books",
			migrations: migrationFiles("20260731120001", "books"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := migrations.Collect(configured); err == nil {
		t.Fatal("Collect() accepted duplicate migration identity")
	}
}

func TestCatalogRejectsMissingPairsAndInvalidNames(t *testing.T) {
	tests := []struct {
		name  string
		files fstest.MapFS
	}{
		{
			name: "missing down",
			files: fstest.MapFS{
				"20260731120001_books.tx.up.sql": {Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "invalid name",
			files: fstest.MapFS{
				"books.up.sql":   {Data: []byte("SELECT 1;")},
				"books.down.sql": {Data: []byte("SELECT 1;")},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configured, err := project.New(
				fixtureSettings{},
				fixtureApp{name: "books", migrations: test.files},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := migrations.Collect(configured); err == nil {
				t.Fatal("Collect() error = nil")
			}
		})
	}
}

func TestAppsWithoutMigrationsNeedNoProvider(t *testing.T) {
	configured, err := project.New(
		fixtureSettings{},
		plainApp{name: "books"},
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := migrations.Collect(configured)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(catalog.Names()) != 0 {
		t.Fatalf("Names() = %v", catalog.Names())
	}
}
