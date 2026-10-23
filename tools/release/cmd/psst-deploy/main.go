// psst-deploy is the installed Linux VPS updater. It is intentionally separate
// from development tooling and accepts only version/status through forced SSH.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/endorses/psst.zip/tools/release/internal/deployment"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	code, err := deployment.Run(ctx, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
