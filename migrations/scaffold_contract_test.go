package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bon5co/godjango/migrations"
)

func TestScaffolderCreatesDeterministicTransactionalPair(t *testing.T) {
	directory := t.TempDir()
	scaffolder := migrations.Scaffolder{
		Now: func() time.Time {
			return time.Date(2026, 7, 31, 12, 34, 56, 0, time.FixedZone("JST", 9*60*60))
		},
	}

	files, err := scaffolder.Create(directory, "add_books")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wantUp := filepath.Join(directory, "20260731033456_add_books.tx.up.sql")
	wantDown := filepath.Join(directory, "20260731033456_add_books.tx.down.sql")
	if files.UpPath != wantUp || files.DownPath != wantDown {
		t.Fatalf("Create() paths = %+v", files)
	}
	for _, path := range []string{files.UpPath, files.DownPath} {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(content) != "-- Write SQL here.\n" {
			t.Errorf("%s content = %q", path, content)
		}
	}
}

func TestScaffolderRejectsInvalidNamesAndCollisions(t *testing.T) {
	scaffolder := migrations.Scaffolder{
		Now: func() time.Time {
			return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
		},
	}
	for _, name := range []string{"", "Add Books", "../books", "books!"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if _, err := scaffolder.Create(t.TempDir(), name); err == nil {
				t.Fatalf("Create(%q) error = nil", name)
			}
		})
	}

	directory := t.TempDir()
	if _, err := scaffolder.Create(directory, "books"); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffolder.Create(directory, "books"); err == nil {
		t.Fatal("second Create() overwrote migration pair")
	}
}
