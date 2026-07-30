//go:build unix

package management

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func runAttached(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	forward := make(chan os.Signal, 1)
	signal.Notify(forward, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(forward)

	if err := command.Start(); err != nil {
		return err
	}
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()

	forwardedCode := 0
	for {
		select {
		case err := <-waited:
			if forwardedCode != 0 {
				return &forwardedSignalError{code: forwardedCode, err: err}
			}
			return err
		case received := <-forward:
			unixSignal, ok := received.(syscall.Signal)
			if !ok {
				unixSignal = syscall.SIGINT
			}
			forwardedCode = 128 + int(unixSignal)
			if err := syscall.Kill(-command.Process.Pid, unixSignal); err != nil &&
				!errors.Is(err, os.ErrProcessDone) {
				_ = command.Process.Kill()
			}
		case <-ctx.Done():
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-waited:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				<-waited
			}
			return ctx.Err()
		}
	}
}

func processExitCode(exitErr *exec.ExitError) int {
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return exitErr.ExitCode()
}
