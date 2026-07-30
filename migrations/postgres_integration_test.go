//go:build integration

package migrations_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/migrations"
	"github.com/bon5co/godjango/project"
)

func migrationTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("GODJANGO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("GODJANGO_TEST_DATABASE_URL is required for integration tests")
	}
	return dsn
}

func migrationDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(
		context.Background(),
		database.DefaultConfig(migrationTestDSN(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func runnerFor(
	t *testing.T,
	db *database.DB,
	files fstest.MapFS,
	tableSuffix string,
) *migrations.Runner {
	t.Helper()
	configured, err := project.New(
		fixtureSettings{},
		fixtureApp{name: "books", migrations: files},
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := migrations.Collect(configured)
	if err != nil {
		t.Fatal(err)
	}
	config := migrations.DefaultRunnerConfig()
	config.Table += "_" + tableSuffix
	config.LocksTable += "_" + tableSuffix
	runner, err := migrations.NewRunner(db, catalog, config)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestPostgresApplyNoopStatusAndRollback(t *testing.T) {
	db := migrationDB(t)
	files := fstest.MapFS{
		"20260731121001_create_probe.tx.up.sql": {
			Data: []byte("CREATE TABLE godjango_migration_probe (value text NOT NULL);\n"),
		},
		"20260731121001_create_probe.tx.down.sql": {
			Data: []byte("DROP TABLE godjango_migration_probe;\n"),
		},
		"20260731121002_insert_probe.tx.up.sql": {
			Data: []byte("INSERT INTO godjango_migration_probe VALUES ('persisted');\n"),
		},
		"20260731121002_insert_probe.tx.down.sql": {
			Data: []byte("DELETE FROM godjango_migration_probe WHERE value = 'persisted';\n"),
		},
	}
	runner := runnerFor(t, db, files, "happy")
	ctx := context.Background()

	applied, err := runner.Apply(ctx)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if strings.Join(applied, ",") !=
		"20260731121001_create_probe,20260731121002_insert_probe" {
		t.Fatalf("Apply() = %v", applied)
	}
	if reapplied, err := runner.Apply(ctx); err != nil || len(reapplied) != 0 {
		t.Fatalf("second Apply() = %v, %v; want no-op", reapplied, err)
	}

	var values []string
	if err := db.Bun().
		NewRaw("SELECT value FROM godjango_migration_probe ORDER BY value").
		Scan(ctx, &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "persisted" {
		t.Fatalf("persisted rows = %v", values)
	}

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range status {
		if !migration.Applied {
			t.Errorf("Status() has pending migration %+v", migration)
		}
	}

	if _, err := runner.Rollback(ctx, ""); err == nil {
		t.Fatal("Rollback() accepted missing confirmation")
	}
	rolledBack, err := runner.Rollback(ctx, migrations.ConfirmRollback)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if len(rolledBack) != 2 {
		t.Fatalf("Rollback() = %v", rolledBack)
	}
	var relation *string
	if err := db.Bun().
		NewRaw("SELECT to_regclass('godjango_migration_probe')").
		Scan(ctx, &relation); err != nil {
		t.Fatal(err)
	}
	if relation != nil {
		t.Fatalf("probe table survived rollback: %q", *relation)
	}
}

func TestFailedTransactionalMigrationRemainsPending(t *testing.T) {
	db := migrationDB(t)
	files := fstest.MapFS{
		"20260731122001_failure.tx.up.sql": {
			Data: []byte(
				"CREATE TABLE godjango_failed_migration_probe (value text);\n" +
					"INSERT INTO table_that_does_not_exist VALUES (1);\n",
			),
		},
		"20260731122001_failure.tx.down.sql": {
			Data: []byte("DROP TABLE godjango_failed_migration_probe;\n"),
		},
	}
	runner := runnerFor(t, db, files, "failure")
	ctx := context.Background()

	if _, err := runner.Apply(ctx); err == nil ||
		!strings.Contains(err.Error(), "20260731122001") {
		t.Fatalf("Apply() error = %v, want migration identity", err)
	}
	var relation *string
	if err := db.Bun().
		NewRaw("SELECT to_regclass('godjango_failed_migration_probe')").
		Scan(ctx, &relation); err != nil {
		t.Fatal(err)
	}
	if relation != nil {
		t.Fatalf("failed transaction left table %q", *relation)
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].Applied {
		t.Fatalf("Status() = %+v, want one pending migration", status)
	}
}

func TestConcurrentPostgresRunnerRejectsSecondLock(t *testing.T) {
	db := migrationDB(t)
	files := fstest.MapFS{
		"20260731123001_slow.tx.up.sql": {
			Data: []byte("SELECT pg_sleep(0.5);\n"),
		},
		"20260731123001_slow.tx.down.sql": {
			Data: []byte("SELECT 1;\n"),
		},
	}
	first := runnerFor(t, db, files, "concurrent")
	second := runnerFor(t, db, files, "concurrent")
	ctx := context.Background()
	if _, err := first.Rollback(ctx, migrations.ConfirmRollback); err != nil {
		t.Fatalf("pre-test Rollback() error = %v", err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	errorsSeen := make(chan error, 2)
	for _, runner := range []*migrations.Runner{first, second} {
		go func(runner *migrations.Runner) {
			defer wait.Done()
			<-start
			_, err := runner.Apply(ctx)
			errorsSeen <- err
		}(runner)
	}
	close(start)
	wait.Wait()
	close(errorsSeen)

	successes := 0
	lockFailures := 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case strings.Contains(err.Error(), "locked") ||
			strings.Contains(err.Error(), "duplicate"):
			lockFailures++
		default:
			t.Errorf("unexpected Apply() error = %v", err)
		}
	}
	if successes != 1 || lockFailures != 1 {
		t.Fatalf("concurrent results: successes=%d lockFailures=%d", successes, lockFailures)
	}
	if _, err := first.Rollback(ctx, migrations.ConfirmRollback); err != nil {
		t.Fatalf("cleanup Rollback() error = %v", err)
	}
}
