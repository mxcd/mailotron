package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mxcd/mailotron/internal/render"
	"github.com/mxcd/mailotron/internal/store"
	cli "github.com/urfave/cli/v3"
)

func readSource(file string) (string, error) {
	switch file {
	case "":
		return "", nil
	case "-":
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	default:
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %q: %w", file, err)
		}
		return string(b), nil
	}
}

// ---- template -----------------------------------------------------------

func templateCommand() *cli.Command {
	return &cli.Command{
		Name:  "template",
		Usage: "manage MJML frame templates",
		Commands: []*cli.Command{
			{Name: "list", Usage: "list templates", Action: templateList},
			{
				Name:   "show",
				Usage:  "show a template; --vars lists required variables",
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "vars", Usage: "annotate with required variables"}},
				Action: templateShow,
			},
			{
				Name:   "add",
				Usage:  "add/overwrite a template from --file (or '-' for stdin)",
				Flags:  []cli.Flag{&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "source .mjml file ('-' = stdin)"}},
				Action: templateAdd,
			},
			{Name: "rm", Usage: "remove a template", Action: templateRm},
		},
	}
}

func templateList(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	st, err := store.New()
	if err != nil {
		return err
	}
	names, err := st.ListTemplates()
	if err != nil {
		return err
	}
	return p.result(map[string]any{"templates": names}, strings.Join(names, "\n"))
}

func templateShow(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("template name required")
	}
	st, err := store.New()
	if err != nil {
		return err
	}
	content, err := st.ReadTemplate(name)
	if err != nil {
		return err
	}
	vars := render.UserVars(content)
	if p.isJSON() {
		return p.emitJSON(map[string]any{"name": name, "mjml": content, "variables": vars})
	}
	if cmd.Bool("vars") {
		fmt.Fprintf(p.out, "# variables: %s\n\n", strings.Join(vars, ", "))
	}
	fmt.Fprintln(p.out, content)
	return nil
}

func templateAdd(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("template name required")
	}
	content, err := readSource(cmd.String("file"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return usageErr("no template content (use --file <path> or --file -)")
	}
	st, err := store.New()
	if err != nil {
		return err
	}
	if err := st.WriteTemplate(name, content); err != nil {
		return err
	}
	path, _ := st.TemplatePath(name)
	return p.result(
		map[string]any{"name": name, "path": path, "variables": render.UserVars(content)},
		"wrote "+path,
	)
}

func templateRm(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("template name required")
	}
	st, err := store.New()
	if err != nil {
		return err
	}
	if err := st.RemoveTemplate(name); err != nil {
		return err
	}
	return p.result(map[string]any{"removed": name}, "removed template "+name)
}

// ---- signature ----------------------------------------------------------

func signatureCommand() *cli.Command {
	return &cli.Command{
		Name:  "signature",
		Usage: "manage MJML signatures",
		Commands: []*cli.Command{
			{Name: "list", Usage: "list signatures", Action: signatureList},
			{Name: "show", Usage: "show a signature (mjml + text)", Action: signatureShow},
			{
				Name:   "add",
				Usage:  "add/overwrite a signature from --file (or '-' for stdin)",
				Flags:  []cli.Flag{&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "source .mjml file ('-' = stdin)"}},
				Action: signatureAdd,
			},
			{Name: "rm", Usage: "remove a signature", Action: signatureRm},
		},
	}
}

func signatureList(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	st, err := store.New()
	if err != nil {
		return err
	}
	names, err := st.ListSignatures()
	if err != nil {
		return err
	}
	return p.result(map[string]any{"signatures": names}, strings.Join(names, "\n"))
}

func signatureShow(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("signature name required")
	}
	st, err := store.New()
	if err != nil {
		return err
	}
	sig, err := st.ReadSignature(name)
	if err != nil {
		return err
	}
	vars := render.UserVars(sig.MJML)
	if p.isJSON() {
		return p.emitJSON(map[string]any{"name": name, "mjml": sig.MJML, "variables": vars})
	}
	fmt.Fprintf(p.out, "# %s\n%s\n", name, sig.MJML)
	return nil
}

func signatureAdd(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("signature name required")
	}
	mjml, err := readSource(cmd.String("file"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(mjml) == "" {
		return usageErr("no signature content (use --file <path> or --file -)")
	}
	st, err := store.New()
	if err != nil {
		return err
	}
	if err := st.WriteSignature(name, mjml); err != nil {
		return err
	}
	return p.result(map[string]any{"name": name}, "wrote signature "+name)
}

func signatureRm(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("signature name required")
	}
	st, err := store.New()
	if err != nil {
		return err
	}
	if err := st.RemoveSignature(name); err != nil {
		return err
	}
	return p.result(map[string]any{"removed": name}, "removed signature "+name)
}
