//go:build integration

package database_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bon5co/godjango/database"
	"github.com/uptrace/bun"
)

func integrationDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("GODJANGO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("GODJANGO_TEST_DATABASE_URL is required for integration tests")
	}
	return dsn
}

func TestPostgresPoolConfigurationAndCloseOwnership(t *testing.T) {
	config := database.DefaultConfig(integrationDSN(t))
	config.MaxOpenConns = 3
	config.MaxIdleConns = 2

	db, err := database.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if got := db.Bun().DB.Stats().MaxOpenConnections; got != 3 {
		t.Errorf("MaxOpenConnections = %d, want 3", got)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestRunInTxCommitsAndRollsBackRows(t *testing.T) {
	config := database.DefaultConfig(integrationDSN(t))
	config.MaxOpenConns = 1
	config.MaxIdleConns = 1
	db, err := database.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.Bun().ExecContext(
		ctx,
		"CREATE TEMP TABLE transaction_probe (value text) ON COMMIT PRESERVE ROWS",
	); err != nil {
		t.Fatal(err)
	}

	if err := database.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO transaction_probe VALUES (?)", "committed")
		return err
	}); err != nil {
		t.Fatalf("committing RunInTx() error = %v", err)
	}

	rollbackErr := errors.New("rollback")
	err = database.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO transaction_probe VALUES (?)", "rolled-back"); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rolling back RunInTx() error = %v", err)
	}

	var values []string
	if err := db.Bun().NewRaw("SELECT value FROM transaction_probe ORDER BY value").Scan(ctx, &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "committed" {
		t.Fatalf("persisted rows = %v, want [committed]", values)
	}
}

func TestExpiredKilledIdleConnectionIsReplaced(t *testing.T) {
	dsn := integrationDSN(t)
	config := database.DefaultConfig(dsn)
	config.MaxOpenConns = 1
	config.MaxIdleConns = 1
	config.ConnMaxIdleTime = 50 * time.Millisecond
	db, err := database.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	adminConfig := database.DefaultConfig(dsn)
	adminConfig.MaxOpenConns = 1
	adminConfig.MaxIdleConns = 1
	admin, err := database.Open(context.Background(), adminConfig)
	if err != nil {
		t.Fatalf("admin Open() error = %v", err)
	}
	defer admin.Close()

	ctx := context.Background()
	var originalPID int
	if err := db.Bun().NewRaw("SELECT pg_backend_pid()").Scan(ctx, &originalPID); err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := admin.Bun().NewRaw("SELECT pg_terminate_backend(?)", originalPID).Scan(ctx, &terminated); err != nil {
		t.Fatal(err)
	}
	if !terminated {
		t.Fatalf("pg_terminate_backend(%d) = false", originalPID)
	}

	// database/sql enforces a minimum connection-cleaner cadence. Wait beyond
	// that cadence so the 50ms idle policy has been applied before reuse.
	time.Sleep(1500 * time.Millisecond)
	var replacementPID int
	if err := db.Bun().NewRaw("SELECT pg_backend_pid()").Scan(ctx, &replacementPID); err != nil {
		t.Fatalf("query after idle recycling error = %v", err)
	}
	if replacementPID == originalPID {
		t.Fatalf("backend PID remained %d after termination", replacementPID)
	}
}
