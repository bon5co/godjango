package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/bon5co/godjango/management"
)

var version = "(devel)"

func main() {
	if version == "(devel)" {
		if build, ok := debug.ReadBuildInfo(); ok && build.Main.Version != "" {
			version = build.Main.Version
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(management.ExecuteGlobal(
		ctx,
		os.Args[1:],
		management.GlobalOptions{Version: version},
		management.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr},
	))
}
