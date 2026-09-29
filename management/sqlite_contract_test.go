package management

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartProjectSQLiteMigratesWithoutDatabaseURL(t *testing.T) {
	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := (Scaffolder{FrameworkVersion: "v0.0.0", FrameworkReplace: frameworkRoot, Database: "sqlite"}).StartProject(context.Background(), t.TempDir(), "bookshelf")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(filepath.Join(root, "internal/project/settings.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), `env.Secret("sqlite:./db.sqlite")`) {
		t.Fatal("SQLite default missing")
	}
	command := exec.Command("go", "run", "./cmd/manage", "migrate")
	command.Dir = root
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "DATABASE_URL=") {
			command.Env = append(command.Env, item)
		}
	}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("migrate: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "Applied 20260731130000_auth") {
		t.Fatalf("migrate output: %s", output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "db.sqlite")); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalStartProjectAcceptsSQLiteSelection(t *testing.T) {
	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	var output bytes.Buffer
	code := ExecuteGlobal(context.Background(), []string{"startproject", "--db", "sqlite", "bookshelf"}, GlobalOptions{
		Version: "v0.0.0", WorkingDirectory: parent, FrameworkReplace: frameworkRoot,
	}, Streams{Out: &output, Err: &output})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, output.String())
	}
	settings, err := os.ReadFile(filepath.Join(parent, "bookshelf", "internal", "project", "settings.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), `env.Secret("sqlite:./db.sqlite")`) {
		t.Fatal("SQLite default missing")
	}
}
