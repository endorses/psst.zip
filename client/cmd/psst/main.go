package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/endorses/psst.zip/client/internal/cli"
)

var version = "dev"
var revision = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := cli.App{Version: version, Revision: revision}
	os.Exit(app.Run(ctx, os.Args[1:]))
}
