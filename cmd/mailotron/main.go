package main

import (
	"context"
	"os"

	mcli "github.com/mxcd/mailotron/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app := mcli.New(version, os.Stdout, os.Stderr)
	err := app.Run(context.Background(), os.Args)
	os.Exit(mcli.HandleError(app, os.Args, err))
}
