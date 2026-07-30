// Package management provides GoDjangGo's project-aware management commands.
package management

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const ProjectMarker = ".godjango"

var ErrProjectNotFound = errors.New("godjango: project not found")

// Streams are attached directly to child management processes.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// ExitError preserves the non-zero status returned by an underlying command.
type ExitError struct {
	Code int
	Err  error
}

func (err *ExitError) Error() string {
	return fmt.Sprintf("godjango: command exited with status %d: %v", err.Code, err.Err)
}

func (err *ExitError) Unwrap() error {
	return err.Err
}

// DiscoverProject walks from start toward the filesystem root looking for the
// marker written by startproject.
func DiscoverProject(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(current); statErr != nil {
		return "", statErr
	} else if !info.IsDir() {
		current = filepath.Dir(current)
	}

	for {
		marker := filepath.Join(current, ProjectMarker)
		if info, statErr := os.Stat(marker); statErr == nil && !info.IsDir() {
			return current, nil
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf(
				"%w from %q; enter a GoDjangGo project or create one with godjango startproject <name>",
				ErrProjectNotFound,
				start,
			)
		}
		current = parent
	}
}

// UnitTestArguments translates only GoDjangGo's optional "--" separator. All
// other arguments remain Go tool arguments.
func UnitTestArguments(args []string) []string {
	if len(args) == 0 {
		return []string{"test", "./..."}
	}
	result := make([]string, 1, len(args)+1)
	result[0] = "test"
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		result = append(result, arg)
	}
	return result
}

// RunUnitTests discovers the project and delegates to the Go test runner. It
// deliberately does not load settings, connect to PostgreSQL, run migrations,
// or enable integration and browser tags.
func RunUnitTests(ctx context.Context, workingDirectory string, args []string, streams Streams) error {
	root, err := DiscoverProject(workingDirectory)
	if err != nil {
		return err
	}
	command := exec.Command("go", UnitTestArguments(args)...)
	command.Dir = root
	command.Env = os.Environ()
	command.Stdin = streams.In
	command.Stdout = defaultWriter(streams.Out, os.Stdout)
	command.Stderr = defaultWriter(streams.Err, os.Stderr)

	err = runAttached(ctx, command)
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if code, forwarded := forwardedExitCode(err); forwarded {
		return &ExitError{Code: code, Err: err}
	}
	var processErr *exec.ExitError
	if errors.As(err, &processErr) {
		return &ExitError{Code: processExitCode(processErr), Err: err}
	}
	if errors.Is(err, exec.ErrNotFound) || strings.Contains(err.Error(), "executable file not found") {
		return fmt.Errorf("godjango test: Go toolchain is unavailable: %w", err)
	}
	return err
}

func defaultWriter(configured io.Writer, fallback io.Writer) io.Writer {
	if configured != nil {
		return configured
	}
	return fallback
}
