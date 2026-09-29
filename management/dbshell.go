package management

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func RunDatabaseShell(
	ctx context.Context,
	dsn string,
	args []string,
	streams Streams,
) error {
	if dsn == "" {
		return errors.New("godjango dbshell: database URL is required")
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	program := "psql"
	connection := dsn
	if strings.HasPrefix(dsn, "sqlite:") {
		program = "sqlite3"
		connection = strings.TrimPrefix(dsn, "sqlite:")
		if strings.HasPrefix(connection, "///") {
			connection = connection[2:]
		}
	}
	tool, err := exec.LookPath(program)
	if err != nil {
		return fmt.Errorf("godjango dbshell: install %s: %w", program, err)
	}
	commandArgs := append([]string{connection}, args...)
	command := exec.Command(tool, commandArgs...)
	command.Env = os.Environ()
	streams = streams.withDefaults()
	command.Stdin = streams.In
	command.Stdout = streams.Out
	command.Stderr = streams.Err
	err = runAttached(ctx, command)
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if code, forwarded := forwardedExitCode(err); forwarded {
		return &ExitError{Code: code, Err: fmt.Errorf("%s interrupted", program)}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExitError{Code: processExitCode(exitErr), Err: fmt.Errorf("%s failed", program)}
	}
	return err
}
