package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/ssubedir/draincheck/internal/cli"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	build := cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		build = resolveBuildInfo(build, info)
	}

	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, build))
}
