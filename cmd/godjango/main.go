package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/bon5co/godjango/management"
)

var version = "(devel)"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(management.ExecuteGlobal(
		ctx,
		os.Args[1:],
		management.GlobalOptions{Version: version},
		management.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr},
	))
}
