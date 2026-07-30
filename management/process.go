package management

import "fmt"

type forwardedSignalError struct {
	code int
	err  error
}

func (err *forwardedSignalError) Error() string {
	return fmt.Sprintf("godjango: command interrupted with status %d: %v", err.code, err.err)
}

func (err *forwardedSignalError) Unwrap() error {
	return err.err
}

func forwardedExitCode(err error) (int, bool) {
	signalErr, ok := err.(*forwardedSignalError)
	if !ok {
		return 0, false
	}
	return signalErr.code, true
}
