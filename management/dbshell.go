package management

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	psql, err := exec.LookPath("psql")
	if err != nil {
		return fmt.Errorf("godjango dbshell: install PostgreSQL psql: %w", err)
	}
	commandArgs := append([]string{dsn}, args...)
	command := exec.Command(psql, commandArgs...)
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
		return &ExitError{Code: code, Err: errors.New("psql interrupted")}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExitError{Code: processExitCode(exitErr), Err: errors.New("psql failed")}
	}
	return err
}
