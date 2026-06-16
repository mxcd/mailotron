// Package cli wires the mailotron command tree (urfave/cli v3). Every command
// supports `-o json` for machine-readable output; errors are formatted centrally
// by HandleError.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	cli "github.com/urfave/cli/v3"
)

const rootHint = "mailotron — email for AI agents.\n" +
	"Run `mailotron guide` for full machine-usable instructions, or `mailotron --help`."

// New builds the root command. out/errw are the stdout/stderr writers (injected
// for testability).
func New(version string, out, errw io.Writer) *cli.Command {
	root := &cli.Command{
		Name:      "mailotron",
		Usage:     "compose, send and manage email — built for AI agents",
		Version:   version,
		Writer:    out,
		ErrWriter: errw,
		// Do not os.Exit inside Run; main formats the error and sets the code.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags:          globalFlags(),
		Commands: []*cli.Command{
			guideCommand(),
			configCommand(),
			templateCommand(),
			signatureCommand(),
			accountCommand(),
			renderCommand(),
			sendCommand(),
			folderCommand(),
			messageCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			fmt.Fprintln(cmd.Root().Writer, rootHint)
			return nil
		},
	}
	// Slice flags (--var, --to, --cc, --bcc, --attach) are repeatable; they must
	// NOT comma-split their values, since an address display name or a variable
	// value may legitimately contain a comma. This setting is read per-command,
	// so apply it across the whole command tree.
	disableSliceSplitting(root)
	return root
}

func disableSliceSplitting(cmd *cli.Command) {
	cmd.DisableSliceFlagSeparator = true
	for _, sub := range cmd.Commands {
		disableSliceSplitting(sub)
	}
}

func globalFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "config", Usage: "config file path (default ~/.mailotron/config.yml)"},
		&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Value: "text", Usage: "output format: text|json"},
		&cli.StringFlag{Name: "account", Aliases: []string{"a"}, Usage: "account name (default: defaults.account)"},
		&cli.BoolFlag{Name: "verbose", Usage: "verbose diagnostics on stderr"},
	}
}

// HandleError formats a command error (text or JSON to stderr) and returns the
// process exit code. Usage errors (cli.Exit code 2) are preserved; other errors
// map to code 1.
func HandleError(app *cli.Command, args []string, err error) int {
	if err == nil {
		return 0
	}
	code := 1
	if ec, ok := err.(cli.ExitCoder); ok {
		code = ec.ExitCode()
	}
	errw := app.ErrWriter
	if errw == nil {
		errw = os.Stderr
	}
	if jsonMode(app, args) {
		enc := json.NewEncoder(errw)
		_ = enc.Encode(map[string]string{"error": err.Error()})
	} else {
		fmt.Fprintln(errw, err.Error())
	}
	return code
}

// jsonMode reports whether JSON output was requested, robust to whether the
// parsed flag survived an early parse error.
func jsonMode(app *cli.Command, args []string) bool {
	if app.String("output") == "json" {
		return true
	}
	for i, a := range args {
		if (a == "-o" || a == "--output") && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
		if a == "-o=json" || a == "--output=json" {
			return true
		}
	}
	return false
}
