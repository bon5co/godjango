//go:build unix

package management

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseShellDelegatesToPsqlAndPreservesStatus(t *testing.T) {
	directory := t.TempDir()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	psql := filepath.Join(directory, "psql")
	script := "#!" + shell + "\nprintf '%s\\n' \"$@\"\nexit \"${GODJANGO_PS_EXIT:-0}\"\n"
	if err := os.WriteFile(psql, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	var output bytes.Buffer
	err = RunDatabaseShell(
		context.Background(),
		"postgres://example.invalid/books",
		[]string{"--", "-c", "select 1"},
		Streams{Out: &output, Err: &output},
	)
	if err != nil {
		t.Fatalf("RunDatabaseShell: %v", err)
	}
	for _, argument := range []string{"postgres://example.invalid/books", "-c", "select 1"} {
		if !strings.Contains(output.String(), argument) {
			t.Fatalf("output %q does not contain %q", output.String(), argument)
		}
	}

	t.Setenv("GODJANGO_PS_EXIT", "7")
	err = RunDatabaseShell(
		context.Background(),
		"postgres://example.invalid/books",
		nil,
		Streams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}},
	)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want *ExitError", err, err)
	}
	if exitErr.Code != 7 {
		t.Fatalf("exit = %d, want 7", exitErr.Code)
	}
	if strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("error leaked DSN: %v", err)
	}
}
