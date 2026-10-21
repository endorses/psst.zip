package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/endorses/psst.zip/client/internal/cli"
)

var version = "dev"
var revision = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	app := cli.App{Version: version, Revision: revision}
	os.Exit(app.Run(ctx, os.Args[1:]))
}
