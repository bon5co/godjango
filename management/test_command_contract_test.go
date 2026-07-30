package management

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDiscoverProjectFromRootAndNestedDirectory(t *testing.T) {
	root := newFixtureProject(t, "example.com/bookshelf", map[string]string{})
	nested := filepath.Join(root, "apps", "books")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, start := range []string{root, nested} {
		got, err := DiscoverProject(start)
		if err != nil {
			t.Fatalf("DiscoverProject(%q): %v", start, err)
		}
		if got != root {
			t.Fatalf("DiscoverProject(%q) = %q, want %q", start, got, root)
		}
	}
}

func TestDiscoverProjectOutsideProjectIsActionable(t *testing.T) {
	_, err := DiscoverProject(t.TempDir())
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("error = %v, want ErrProjectNotFound", err)
	}
	for _, fragment := range []string{"godjango startproject", "enter"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not contain %q", err, fragment)
		}
	}
}

func TestUnitTestArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "default", want: []string{"test", "./..."}},
		{
			name: "narrow package",
			args: []string{"./apps/books/..."},
			want: []string{"test", "./apps/books/..."},
		},
		{
			name: "verbatim pass through",
			args: []string{"--", "-race", "-count=1", "./..."},
			want: []string{"test", "-race", "-count=1", "./..."},
		},
		{
			name: "package then pass through",
			args: []string{"./apps/books/...", "--", "-run", "TestBook"},
			want: []string{"test", "./apps/books/...", "-run", "TestBook"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := UnitTestArguments(test.args); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("UnitTestArguments(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestRunUnitTestsUsesProjectRootAndPreservesStreams(t *testing.T) {
	root := newFixtureProject(t, "example.com/bookshelf", map[string]string{
		"books/books_test.go": `package books

import (
	"fmt"
	"os"
	"testing"
)

func TestUnitOnly(t *testing.T) {
	if os.Getenv("GODJANGO_TEST_DATABASE_URL") == "" {
		t.Fatal("test sentinel was not inherited")
	}
	fmt.Println("unit suite reached")
}
`,
	})
	nested := filepath.Join(root, "apps", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GODJANGO_TEST_DATABASE_URL", "postgres://must-not-be-opened.invalid/test")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := RunUnitTests(context.Background(), nested, nil, Streams{
		Out: &stdout,
		Err: &stderr,
	})
	if err != nil {
		t.Fatalf("RunUnitTests: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example.com/bookshelf/books") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunUnitTestsPreservesGoTestFailure(t *testing.T) {
	root := newFixtureProject(t, "example.com/failing", map[string]string{
		"failure_test.go": `package failing

import "testing"

func TestFailure(t *testing.T) { t.Fatal("expected failure") }
`,
	})

	var stderr bytes.Buffer
	err := RunUnitTests(context.Background(), root, []string{"--", "-count=1"}, Streams{
		Out: &stderr,
		Err: &stderr,
	})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want *ExitError", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(stderr.String(), "expected failure") {
		t.Fatalf("test output = %q", stderr.String())
	}
}

func TestRunUnitTestsHonorsCancellation(t *testing.T) {
	root := newFixtureProject(t, "example.com/cancelled", map[string]string{
		"cancel_test.go": `package cancelled

import (
	"testing"
	"time"
)

func TestWait(t *testing.T) { time.Sleep(30 * time.Second) }
`,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunUnitTests(ctx, root, nil, Streams{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestRunUnitTestsPreservesCompilationFailure(t *testing.T) {
	root := newFixtureProject(t, "example.com/doesnotcompile", map[string]string{
		"broken.go": "package doesnotcompile\n\nfunc Broken( {\n",
	})

	var stderr bytes.Buffer
	err := RunUnitTests(context.Background(), root, nil, Streams{Out: &bytes.Buffer{}, Err: &stderr})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want *ExitError", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(stderr.String(), "syntax error") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunUnitTestsForwardsInterruptAndStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal contract")
	}
	if raceEnabled {
		t.Skip("the race-instrumented Go test harness owns interrupt handling")
	}
	ready := filepath.Join(t.TempDir(), "ready")
	root := newFixtureProject(t, "example.com/interrupted", map[string]string{
		"interrupt_test.go": `package interrupted

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	_ = m
	if err := os.WriteFile(os.Getenv("GODJANGO_SIGNAL_READY"), []byte("ready"), 0o644); err != nil {
		panic(err)
	}
	for {
		time.Sleep(time.Hour)
	}
}
`,
	})
	t.Setenv("GODJANGO_SIGNAL_READY", ready)
	command := exec.Command(os.Args[0], "-test.run=^TestUnitTestSignalHelper$")
	var helperOutput bytes.Buffer
	command.Stdout = &helperOutput
	command.Stderr = &helperOutput
	command.Env = append(
		os.Environ(),
		"GODJANGO_SIGNAL_HELPER=1",
		"GODJANGO_SIGNAL_ROOT="+root,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			t.Fatal("test process did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		_ = command.Process.Kill()
		t.Fatal(err)
	}
	err := command.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper error = %T %v, want *exec.ExitError", err, err)
	}
	if code := processExitCode(exitErr); code != 130 {
		t.Fatalf("helper exit code = %d, want 130; output = %q", code, helperOutput.String())
	}
}

func TestUnitTestSignalHelper(t *testing.T) {
	if os.Getenv("GODJANGO_SIGNAL_HELPER") != "1" {
		return
	}
	err := RunUnitTests(
		context.Background(),
		os.Getenv("GODJANGO_SIGNAL_ROOT"),
		nil,
		Streams{Out: os.Stdout, Err: os.Stderr},
	)
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}
	if err != nil {
		os.Exit(ExitFailure)
	}
	os.Exit(ExitOK)
}

func newFixtureProject(t *testing.T, module string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module " + module + "\n\ngo 1.26.5\n"
	files[ProjectMarker] = "generated by GoDjangGo\n"
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
