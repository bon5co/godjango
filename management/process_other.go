//go:build !unix

package management

import (
	"context"
	"os/exec"
)

func runAttached(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()
	select {
	case err := <-waited:
		return err
	case <-ctx.Done():
		_ = command.Process.Kill()
		<-waited
		return ctx.Err()
	}
}

func processExitCode(exitErr *exec.ExitError) int {
	return exitErr.ExitCode()
}
