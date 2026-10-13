package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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

// addressFlags are shared by send and draft: recipients, sender overrides and
// attachments.
func addressFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{Name: "to", Usage: "recipient (repeatable; RFC 5322 address)"},
		&cli.StringSliceFlag{Name: "cc", Usage: "cc recipient (repeatable)"},
		&cli.StringSliceFlag{Name: "bcc", Usage: "bcc recipient (repeatable)"},
		&cli.StringFlag{Name: "from", Usage: "override the account's From address"},
		&cli.StringFlag{Name: "reply-to", Usage: "Reply-To address"},
		&cli.StringSliceFlag{Name: "attach", Usage: "attach a file (repeatable)"},
		&cli.StringSliceFlag{Name: "embed", Usage: "inline image cid=path, referenced as cid:<cid> (repeatable)"},
	}
}

func sendCommand() *cli.Command {
	return &cli.Command{
		Name:  "send",
		Usage: "render and send a message",
		Flags: append(append(composeFlags(), addressFlags()...),
			&cli.BoolFlag{Name: "dry-run", Usage: "render but do not send"},
		),
		Action: sendAction,
	}
}

// buildMessage renders the composed input and wraps it with the address and
// attachment flags into a transport-ready message.
func buildMessage(cmd *cli.Command, acc *config.Account, in render.Input) (*email.Message, *render.Output, error) {
	out, err := render.Render(in)
	if err != nil {
		return nil, nil, err
	}
	atts, err := readAttachments(cmd.StringSlice("attach"))
	if err != nil {
		return nil, nil, err
	}
	inline, err := parseEmbeds(cmd.StringSlice("embed"))
	if err != nil {
		return nil, nil, err
	}
	msg := &email.Message{
		From:        firstNonEmpty(cmd.String("from"), acc.From),
		To:          cmd.StringSlice("to"),
		Cc:          cmd.StringSlice("cc"),
		Bcc:         cmd.StringSlice("bcc"),
		ReplyTo:     cmd.String("reply-to"),
		Subject:     in.Subject,
		HTML:        out.HTML,
		Text:        out.Text,
		Attachments: atts,
		Inline:      inline,
	}
	return msg, out, nil
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
	if len(cmd.StringSlice("to")) == 0 {
		return usageErr("--to is required")
	}
	if in.Subject == "" {
		return usageErr("--subject is required")
	}
	msg, out, err := buildMessage(cmd, acc, in)
	if err != nil {
		return err
	}

	if cmd.Bool("dry-run") {
		return p.result(
			map[string]any{"dryRun": true, "account": acc.Name, "message": msg, "html": out.HTML, "text": out.Text},
			fmt.Sprintf("[dry-run] not sent - %d recipients, %d attachment(s)", len(msg.Recipients()), len(msg.Attachments)),
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
		map[string]any{"sent": true, "account": acc.Name, "to": msg.To, "subject": in.Subject, "attachments": len(msg.Attachments)},
		"sent to "+strings.Join(msg.To, ", "),
	)
}

func draftCommand() *cli.Command {
	return &cli.Command{
		Name:  "draft",
		Usage: "render a message and store it as a draft in an IMAP folder (never sends)",
		Flags: append(append(composeFlags(), addressFlags()...),
			&cli.StringFlag{Name: "folder", Usage: "target folder (default: the account's imap.drafts_folder, else Drafts)"},
			&cli.UintFlag{Name: "reply-uid", Usage: "UID of the message this draft answers; sets In-Reply-To/References and defaults the subject to Re: <parent>"},
			&cli.StringFlag{Name: "reply-folder", Value: "INBOX", Usage: "folder holding --reply-uid"},
			&cli.BoolFlag{Name: "no-quote", Usage: "do not quote the --reply-uid message below the reply"},
		),
		Action: draftAction,
	}
}

func draftAction(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	c, acc, err := connectIMAP(cmd, cfg)
	if err != nil {
		return err
	}
	defer c.Close()

	in, err := composeInput(cmd, cfg)
	if err != nil {
		return err
	}
	headers := map[string]string{}
	replyUID := cmd.Uint("reply-uid")
	if replyUID != 0 {
		parent, err := c.Thread(cmd.String("reply-folder"), uint32(replyUID))
		if err != nil {
			return err
		}
		irt, refs := parent.ReplyHeaders()
		if irt != "" {
			headers["In-Reply-To"], headers["References"] = irt, refs
		}
		if in.Subject == "" {
			in.Subject = parent.ReplySubject()
		}
	}
	if in.Subject == "" {
		return usageErr("--subject is required (or --reply-uid to derive it)")
	}
	msg, _, err := buildMessage(cmd, acc, in)
	if err != nil {
		return err
	}
	msg.Headers = headers
	if replyUID != 0 && !cmd.Bool("no-quote") {
		parent, err := c.ShowMessage(cmd.String("reply-folder"), uint32(replyUID), true)
		if err != nil {
			return err
		}
		msg.HTML, msg.Text = render.Quote(msg.HTML, msg.Text, render.Parent{
			From: firstNonEmpty(strings.Join(parent.From, ", "), "unknown"),
			Date: parent.When(), Text: parent.Text, HTML: parent.HTML,
		})
	}

	raw, messageID, err := transport.BuildMIME(msg)
	if err != nil {
		return err
	}
	folder := firstNonEmpty(cmd.String("folder"), acc.IMAP.DraftsOrDefault())
	uid, err := c.AppendMessage(folder, raw, []string{`\Draft`, `\Seen`}, time.Now())
	if err != nil {
		return err
	}
	return p.result(
		map[string]any{
			"drafted": true, "account": acc.Name, "folder": folder, "uid": uid, "messageId": messageID,
			"subject": in.Subject, "to": msg.To, "cc": msg.Cc, "attachments": len(msg.Attachments),
			"inReplyTo": headers["In-Reply-To"],
		},
		fmt.Sprintf("draft %q stored in %s", in.Subject, folder),
	)
}
