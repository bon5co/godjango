package management

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	ExitOK       = 0
	ExitFailure  = 1
	ExitUsage    = 2
	ExitCanceled = 130
)

type GlobalOptions struct {
	Version          string
	WorkingDirectory string
	FrameworkReplace string
}

// ExecuteGlobal runs the installed godjango command. Framework-global
// operations stay here; every project-aware operation is compiled from and
// delegated to the discovered project's cmd/manage program.
func ExecuteGlobal(ctx context.Context, args []string, options GlobalOptions, streams Streams) int {
	streams = streams.withDefaults()
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = io.WriteString(streams.Out, globalHelp)
		return ExitOK
	}
	if args[0] == "--version" || args[0] == "version" {
		version := options.Version
		if version == "" {
			version = "(devel)"
		}
		_, _ = fmt.Fprintf(streams.Out, "godjango %s\n", version)
		return ExitOK
	}

	workingDirectory := options.WorkingDirectory
	if workingDirectory == "" {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintln(streams.Err, err)
			return ExitFailure
		}
	}
	if args[0] == "startproject" {
		if len(args) != 2 {
			_, _ = fmt.Fprintln(streams.Err, "usage: godjango startproject <name>")
			return ExitUsage
		}
		scaffolder := Scaffolder{
			FrameworkVersion: options.Version,
			FrameworkReplace: options.FrameworkReplace,
		}
		root, err := scaffolder.StartProject(ctx, workingDirectory, args[1])
		if err != nil {
			_, _ = fmt.Fprintln(streams.Err, err)
			return ExitFailure
		}
		_, _ = fmt.Fprintf(streams.Out, "Created project %s\n", root)
		return ExitOK
	}

	root, err := DiscoverProject(workingDirectory)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, err)
		return ExitUsage
	}
	return executeManager(ctx, root, args, streams)
}

func executeManager(ctx context.Context, root string, args []string, streams Streams) int {
	err := RunProjectProgram(ctx, root, "./cmd/manage", args, streams)
	if err == nil {
		return ExitOK
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return commandStatus(ctx, err, streams.Err)
}

// RunProjectProgram compiles a project-owned command through the Go build
// cache, then runs it with attached streams and signal forwarding.
func RunProjectProgram(
	ctx context.Context,
	root string,
	packagePath string,
	args []string,
	streams Streams,
) error {
	streams = streams.withDefaults()
	temporary, err := os.CreateTemp("", "godjango-manage-*")
	if err != nil {
		return fmt.Errorf("godjango: create project executable: %w", err)
	}
	programPath := temporary.Name()
	if closeErr := temporary.Close(); closeErr != nil {
		_ = os.Remove(programPath)
		return fmt.Errorf("godjango: create project executable: %w", closeErr)
	}
	if removeErr := os.Remove(programPath); removeErr != nil {
		return fmt.Errorf("godjango: prepare project executable: %w", removeErr)
	}
	defer os.Remove(programPath)

	if packagePath == "" {
		return errors.New("godjango: project package path is required")
	}
	if packagePath[0] != '.' {
		packagePath = "." + string(filepath.Separator) + filepath.Clean(packagePath)
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", programPath, packagePath)
	build.Dir = root
	build.Env = os.Environ()
	build.Stdout = streams.Out
	build.Stderr = streams.Err
	if err := runAttached(ctx, build); err != nil {
		return attachedCommandError(ctx, err)
	}

	command := exec.Command(programPath, args...)
	command.Dir = root
	command.Env = os.Environ()
	command.Stdin = streams.In
	command.Stdout = streams.Out
	command.Stderr = streams.Err
	if err := runAttached(ctx, command); err != nil {
		return attachedCommandError(ctx, err)
	}
	return nil
}

func attachedCommandError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if code, forwarded := forwardedExitCode(err); forwarded {
		return &ExitError{Code: code, Err: err}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExitError{Code: processExitCode(exitErr), Err: err}
	}
	return err
}

func commandStatus(ctx context.Context, err error, stderr io.Writer) int {
	if ctx.Err() != nil {
		return ExitCanceled
	}
	var commandErr *ExitError
	if errors.As(err, &commandErr) {
		return commandErr.Code
	}
	if code, forwarded := forwardedExitCode(err); forwarded {
		return code
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := processExitCode(exitErr)
		if code >= 0 {
			return code
		}
		return ExitCanceled
	}
	_, _ = fmt.Fprintln(stderr, err)
	return ExitFailure
}

func isHelp(argument string) bool {
	return argument == "--help" || argument == "-h" || argument == "help"
}

func (streams Streams) withDefaults() Streams {
	if streams.In == nil {
		streams.In = os.Stdin
	}
	if streams.Out == nil {
		streams.Out = os.Stdout
	}
	if streams.Err == nil {
		streams.Err = os.Stderr
	}
	return streams
}

const globalHelp = `GoDjangGo management utility

Usage:
  godjango startproject <name>
  godjango <project-command> [arguments]

Global commands:
  startproject       Create a GoDjangGo project
  version            Print the installed version

Project commands:
  startapp           Create and explicitly register an application
  runserver          Run the development server
  test               Run project unit tests
  check              Run project and application checks
  makemigration      Create an explicit SQL migration pair
  migrate            Apply pending migrations
  migrationstatus    Show migration status
  createsuperuser    Create an administrative user
  changepassword     Change a user's password
  dbshell            Open a PostgreSQL shell

Projects may explicitly register additional commands.
`
