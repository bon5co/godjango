package main

import (
	"context"
	"os"
	"runtime/debug"

	"github.com/bon5co/godjango/management"
)

var version = "(devel)"

func main() {
	if version == "(devel)" {
		if build, ok := debug.ReadBuildInfo(); ok && build.Main.Version != "" {
			version = build.Main.Version
		}
	}
	os.Exit(management.ExecuteGlobal(
		context.Background(),
		os.Args[1:],
		management.GlobalOptions{Version: version},
		management.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr},
	))
}
