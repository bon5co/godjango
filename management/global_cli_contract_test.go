package management

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalCLIHelpAndVersion(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		version  string
		contains []string
	}{
		{
			name:     "help",
			args:     []string{"--help"},
			contains: []string{"startproject", "startapp", "runserver", "test", "check", "createsuperuser", "changepassword", "makemigration", "migrate", "dbshell"},
		},
		{
			name:     "version",
			args:     []string{"--version"},
			version:  "v0.4.0",
			contains: []string{"godjango v0.4.0"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := ExecuteGlobal(context.Background(), test.args, GlobalOptions{
				Version:          test.version,
				WorkingDirectory: t.TempDir(),
			}, Streams{Out: &stdout, Err: &stderr})
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
			}
			for _, fragment := range test.contains {
				if !strings.Contains(stdout.String(), fragment) {
					t.Fatalf("stdout %q does not contain %q", stdout.String(), fragment)
				}
			}
		})
	}
}

func TestGlobalCLIProjectCommandDelegatesFromNestedDirectory(t *testing.T) {
	root := newManagerFixture(t, `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println("manager:", strings.Join(os.Args[1:], "|"))
}
`)
	nested := filepath.Join(root, "apps", "books")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ExecuteGlobal(
		context.Background(),
		[]string{"test", "--", "-race", "./apps/books/..."},
		GlobalOptions{WorkingDirectory: nested},
		Streams{Out: &stdout, Err: &stderr},
	)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "manager: test|--|-race|./apps/books/..." {
		t.Fatalf("stdout = %q", got)
	}
}

func TestGlobalCLIPreservesManagerExitCode(t *testing.T) {
	root := newManagerFixture(t, `package main

import "os"

func main() { os.Exit(7) }
`)

	code := ExecuteGlobal(
		context.Background(),
		[]string{"custom-command"},
		GlobalOptions{WorkingDirectory: root},
		Streams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}},
	)
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestGlobalCLIOutsideProjectFailsWithUsageStatus(t *testing.T) {
	var stderr bytes.Buffer
	code := ExecuteGlobal(
		context.Background(),
		[]string{"test"},
		GlobalOptions{WorkingDirectory: t.TempDir()},
		Streams{Out: &bytes.Buffer{}, Err: &stderr},
	)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	for _, fragment := range []string{"godjango startproject", "enter"} {
		if !strings.Contains(stderr.String(), fragment) {
			t.Fatalf("stderr %q does not contain %q", stderr.String(), fragment)
		}
	}
}

func newManagerFixture(t *testing.T, manager string) string {
	t.Helper()
	root := newFixtureProject(t, "example.com/managerfixture", map[string]string{
		"cmd/manage/main.go": manager,
	})
	if _, err := os.Stat(filepath.Join(root, "cmd", "manage", "main.go")); err != nil {
		t.Fatal(err)
	}
	return root
}
