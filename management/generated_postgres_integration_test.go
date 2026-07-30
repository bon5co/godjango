//go:build integration

package management

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
)

func TestGeneratedManagementCommandsUseRealPostgresAndReleaseConnections(t *testing.T) {
	baseDSN := os.Getenv("GODJANGO_TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Fatal("GODJANGO_TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	admin, err := database.Open(ctx, database.DefaultConfig(baseDSN))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})

	schema := fmt.Sprintf("godjango_cli_%d", time.Now().UnixNano())
	quotedSchema := `"` + schema + `"`
	if _, err := admin.Bun().ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Bun().ExecContext(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		if err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	applicationName := schema + "_manager"
	query.Set("application_name", applicationName)
	parsed.RawQuery = query.Encode()
	t.Setenv("DATABASE_URL", parsed.String())

	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	projectRoot, err := (Scaffolder{
		FrameworkVersion: "v0.0.0",
		FrameworkReplace: frameworkRoot,
	}).StartProject(ctx, t.TempDir(), "bookshelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := (Scaffolder{}).StartApp(projectRoot, "library"); err != nil {
		t.Fatal(err)
	}

	runGeneratedCommand(t, projectRoot, []string{
		"makemigration", "create_probe", "--app", "library",
	}, "")
	migrationFiles, err := filepath.Glob(filepath.Join(
		projectRoot,
		"apps",
		"library",
		"migrations",
		"*_create_probe.tx.*.sql",
	))
	if err != nil || len(migrationFiles) != 2 {
		t.Fatalf("migration files = %v, %v", migrationFiles, err)
	}
	for _, path := range migrationFiles {
		statement := "CREATE TABLE cli_probe (value text NOT NULL);\n"
		if strings.HasSuffix(path, ".down.sql") {
			statement = "DROP TABLE cli_probe;\n"
		}
		if err := os.WriteFile(path, []byte(statement), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	migrateOutput := runGeneratedCommand(t, projectRoot, []string{"migrate"}, "")
	for _, fragment := range []string{"create_probe", "auth"} {
		if !strings.Contains(migrateOutput, fragment) {
			t.Fatalf("migrate output %q does not contain %q", migrateOutput, fragment)
		}
	}
	statusOutput := runGeneratedCommand(t, projectRoot, []string{"migrationstatus"}, "")
	if !strings.Contains(statusOutput, "[X]") || !strings.Contains(statusOutput, "create_probe") {
		t.Fatalf("migration status = %q", statusOutput)
	}

	username := fmt.Sprintf("root_%d", time.Now().UnixNano())
	runGeneratedCommand(t, projectRoot, []string{
		"createsuperuser",
		"--username", username,
		"--email", "ROOT@EXAMPLE.COM",
		"--password-stdin",
	}, "first-secret\n")
	runGeneratedCommand(t, projectRoot, []string{
		"changepassword", username, "--password-stdin",
	}, "second-secret\n")

	var persisted struct {
		PasswordHash string `bun:"password_hash"`
		Email        string `bun:"email"`
		IsStaff      bool   `bun:"is_staff"`
		IsSuperuser  bool   `bun:"is_superuser"`
	}
	err = admin.Bun().NewRaw(
		"SELECT password_hash, email, is_staff, is_superuser FROM "+
			quotedSchema+".auth_users WHERE username = ?",
		username,
	).Scan(ctx, &persisted)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Email != "ROOT@example.com" || !persisted.IsStaff || !persisted.IsSuperuser {
		t.Fatalf("persisted superuser = %+v", persisted)
	}
	result, err := auth.NewPasswordHasher().Check(pointerTo("second-secret"), persisted.PasswordHash)
	if err != nil || !result.OK {
		t.Fatalf("new password check = %+v, %v", result, err)
	}
	oldResult, err := auth.NewPasswordHasher().Check(pointerTo("first-secret"), persisted.PasswordHash)
	if err != nil || oldResult.OK {
		t.Fatalf("old password check = %+v, %v", oldResult, err)
	}

	var probe *string
	if err := admin.Bun().NewRaw(
		"SELECT to_regclass(?)",
		schema+".cli_probe",
	).Scan(ctx, &probe); err != nil {
		t.Fatal(err)
	}
	if probe == nil {
		t.Fatal("CLI migration did not create cli_probe")
	}
	var active int
	if err := admin.Bun().NewRaw(
		"SELECT count(*) FROM pg_stat_activity WHERE application_name = ?",
		applicationName,
	).Scan(ctx, &active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("management commands left %d database connections open", active)
	}
}

func runGeneratedCommand(t *testing.T, root string, args []string, input string) string {
	t.Helper()
	var output bytes.Buffer
	code := ExecuteGlobal(
		context.Background(),
		args,
		GlobalOptions{WorkingDirectory: root},
		Streams{In: strings.NewReader(input), Out: &output, Err: &output},
	)
	if code != ExitOK {
		t.Fatalf("godjango %s exit = %d\n%s", strings.Join(args, " "), code, output.String())
	}
	return output.String()
}

func pointerTo(value string) *string {
	return &value
}
