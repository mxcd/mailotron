package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	cli "github.com/urfave/cli/v3"
)

// printer renders command output as either human text or JSON, honoring the
// global --output flag. JSON goes to stdout; text goes to stdout; errors are
// handled centrally in main.
type printer struct {
	jsonOut bool
	out     io.Writer
}

func newPrinter(cmd *cli.Command) *printer {
	out := cmd.Root().Writer
	if out == nil {
		out = os.Stdout
	}
	return &printer{jsonOut: cmd.String("output") == "json", out: out}
}

func (p *printer) isJSON() bool { return p.jsonOut }

// emitJSON writes v as indented JSON.
func (p *printer) emitJSON(v any) error {
	enc := json.NewEncoder(p.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// result emits v as JSON in JSON mode, otherwise prints the supplied text.
func (p *printer) result(v any, text string) error {
	if p.jsonOut {
		return p.emitJSON(v)
	}
	if text != "" {
		fmt.Fprintln(p.out, text)
	}
	return nil
}

// usageErr builds an exit-code-2 (usage) error.
func usageErr(format string, args ...any) error {
	return cli.Exit("error: "+fmt.Sprintf(format, args...), 2)
}
