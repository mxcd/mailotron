package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/email"
	"github.com/mxcd/mailotron/internal/render"
	"github.com/mxcd/mailotron/internal/transport"
	cli "github.com/urfave/cli/v3"
)

func composeFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "template", Aliases: []string{"t"}, Usage: "stored template name (default: defaults.template)"},
		&cli.StringFlag{Name: "template-file", Usage: "MJML frame from a file instead of the store"},
		&cli.StringFlag{Name: "signature", Aliases: []string{"s"}, Usage: "stored signature name (default: defaults.signature)"},
		&cli.StringFlag{Name: "signature-file", Usage: "signature MJML from a file"},
		&cli.StringFlag{Name: "body", Usage: "inline body content"},
		&cli.StringFlag{Name: "body-file", Usage: "body from a file ('-' = stdin)"},
		&cli.StringFlag{Name: "body-format", Value: "markdown", Usage: "body format: markdown|mjml|text|html"},
		&cli.StringFlag{Name: "subject", Usage: "email subject"},
		&cli.StringFlag{Name: "preview", Usage: "inbox preview text"},
		&cli.StringFlag{Name: "align", Value: "left", Usage: "email block alignment: left (normal mail) | center"},
		&cli.StringSliceFlag{Name: "var", Usage: "template variable key=value (repeatable)"},
		&cli.StringFlag{Name: "vars-file", Usage: "JSON file of template variables"},
	}
}

// loadConfigOptional returns an empty config if no config file exists, so that
// `render` works statelessly with --template-file.
func loadConfigOptional(cmd *cli.Command) (*config.Config, error) {
	path, err := config.FilePath(cmd.String("config"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &config.Config{}, nil
	}
	return config.Load(cmd.String("config"))
}

func renderCommand() *cli.Command {
	return &cli.Command{
		Name:  "render",
		Usage: "render a message without sending (outputs html, text, mjml)",
		Flags: append(composeFlags(),
			&cli.StringFlag{Name: "html-out", Usage: "write rendered HTML to this file"},
			&cli.StringFlag{Name: "text-out", Usage: "write plain text to this file"},
		),
		Action: renderAction,
	}
}

func renderAction(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfigOptional(cmd)
	if err != nil {
		return err
	}
	in, err := composeInput(cmd, cfg)
	if err != nil {
		return err
	}
	out, err := render.Render(in)
	if err != nil {
		return err
	}
	if f := cmd.String("html-out"); f != "" {
		if err := os.WriteFile(f, []byte(out.HTML), 0o644); err != nil {
			return err
		}
	}
	if f := cmd.String("text-out"); f != "" {
		if err := os.WriteFile(f, []byte(out.Text), 0o644); err != nil {
			return err
		}
	}
	return p.result(out, out.Text)
}

func sendCommand() *cli.Command {
	return &cli.Command{
		Name:  "send",
		Usage: "render and send a message",
		Flags: append(composeFlags(),
			&cli.StringSliceFlag{Name: "to", Usage: "recipient (repeatable; RFC 5322 address)"},
			&cli.StringSliceFlag{Name: "cc", Usage: "cc recipient (repeatable)"},
			&cli.StringSliceFlag{Name: "bcc", Usage: "bcc recipient (repeatable)"},
			&cli.StringFlag{Name: "from", Usage: "override the account's From address"},
			&cli.StringFlag{Name: "reply-to", Usage: "Reply-To address"},
			&cli.StringSliceFlag{Name: "attach", Usage: "attach a file (repeatable)"},
			&cli.StringSliceFlag{Name: "embed", Usage: "inline image cid=path, referenced as cid:<cid> (repeatable)"},
			&cli.BoolFlag{Name: "dry-run", Usage: "render but do not send"},
		),
		Action: sendAction,
	}
}

func sendAction(ctx context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	acc, err := resolveAccount(cmd, cfg)
	if err != nil {
		return err
	}
	in, err := composeInput(cmd, cfg)
	if err != nil {
		return err
	}
	out, err := render.Render(in)
	if err != nil {
		return err
	}

	to := cmd.StringSlice("to")
	if len(to) == 0 {
		return usageErr("--to is required")
	}
	if in.Subject == "" {
		return usageErr("--subject is required")
	}
	atts, err := readAttachments(cmd.StringSlice("attach"))
	if err != nil {
		return err
	}
	inline, err := parseEmbeds(cmd.StringSlice("embed"))
	if err != nil {
		return err
	}

	msg := &email.Message{
		From:        firstNonEmpty(cmd.String("from"), acc.From),
		To:          to,
		Cc:          cmd.StringSlice("cc"),
		Bcc:         cmd.StringSlice("bcc"),
		ReplyTo:     cmd.String("reply-to"),
		Subject:     in.Subject,
		HTML:        out.HTML,
		Text:        out.Text,
		Attachments: atts,
		Inline:      inline,
	}

	if cmd.Bool("dry-run") {
		return p.result(
			map[string]any{"dryRun": true, "account": acc.Name, "message": msg, "html": out.HTML, "text": out.Text},
			fmt.Sprintf("[dry-run] not sent — %d recipients, %d attachment(s)", len(msg.Recipients()), len(atts)),
		)
	}

	sender, err := transport.ForAccount(acc)
	if err != nil {
		return err
	}
	if err := sender.Send(ctx, msg); err != nil {
		return err
	}
	return p.result(
		map[string]any{"sent": true, "account": acc.Name, "to": to, "subject": in.Subject, "attachments": len(atts)},
		"sent to "+strings.Join(to, ", "),
	)
}
