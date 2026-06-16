package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mxcd/mailotron/internal/agentguide"
	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/imapclient"
	"github.com/mxcd/mailotron/internal/store"
	"github.com/mxcd/mailotron/internal/transport"
	cli "github.com/urfave/cli/v3"
)

// ---- guide --------------------------------------------------------------

func guideCommand() *cli.Command {
	return &cli.Command{
		Name:    "guide",
		Aliases: []string{"agent", "llm"},
		Usage:   "print agent usage instructions (Markdown; -o json yields a command catalog)",
		Action: func(_ context.Context, cmd *cli.Command) error {
			p := newPrinter(cmd)
			if p.isJSON() {
				return p.emitJSON(map[string]any{
					"guide":    agentguide.Markdown(),
					"meta":     agentguide.GuideMeta(),
					"commands": buildCatalog(cmd.Root()),
				})
			}
			fmt.Fprintln(p.out, agentguide.Markdown())
			return nil
		},
	}
}

type flagInfo struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Usage   string   `json:"usage,omitempty"`
}

type cmdInfo struct {
	Path        string     `json:"path"`
	Usage       string     `json:"usage,omitempty"`
	Aliases     []string   `json:"aliases,omitempty"`
	Flags       []flagInfo `json:"flags,omitempty"`
	Subcommands []cmdInfo  `json:"subcommands,omitempty"`
}

func buildCatalog(root *cli.Command) []cmdInfo {
	var walk func(c *cli.Command, prefix string) cmdInfo
	walk = func(c *cli.Command, prefix string) cmdInfo {
		path := strings.TrimSpace(prefix + " " + c.Name)
		ci := cmdInfo{Path: path, Usage: c.Usage, Aliases: c.Aliases}
		for _, f := range c.Flags {
			ci.Flags = append(ci.Flags, flagInfoFromFlag(f))
		}
		for _, sub := range c.Commands {
			ci.Subcommands = append(ci.Subcommands, walk(sub, path))
		}
		return ci
	}
	out := make([]cmdInfo, 0, len(root.Commands))
	for _, c := range root.Commands {
		out = append(out, walk(c, root.Name))
	}
	return out
}

func flagInfoFromFlag(f cli.Flag) flagInfo {
	names := f.Names()
	fi := flagInfo{}
	if len(names) > 0 {
		fi.Name = names[0]
		if len(names) > 1 {
			fi.Aliases = names[1:]
		}
	}
	if uf, ok := f.(interface{ GetUsage() string }); ok {
		fi.Usage = uf.GetUsage()
	}
	return fi
}

// ---- config -------------------------------------------------------------

func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "manage configuration",
		Commands: []*cli.Command{
			{
				Name:   "init",
				Usage:  "write a starter config and seed default templates/signatures",
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "force", Usage: "overwrite existing files"}},
				Action: configInit,
			},
			{Name: "path", Usage: "print the resolved config file path", Action: configPath},
			{Name: "validate", Usage: "validate config and referenced env vars", Action: configValidate},
		},
	}
}

func configInit(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	dst, err := store.InitConfig(cmd.String("config"), cmd.Bool("force"))
	if err != nil {
		return err
	}
	st, err := store.New()
	if err != nil {
		return err
	}
	seeded, err := st.Seed(cmd.Bool("force"))
	if err != nil {
		return err
	}
	return p.result(
		map[string]any{"config": dst, "seeded": seeded},
		fmt.Sprintf("wrote %s\nseeded %d default asset(s): %s", dst, len(seeded), strings.Join(seeded, ", ")),
	)
}

func configPath(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	path, err := config.FilePath(cmd.String("config"))
	if err != nil {
		return err
	}
	return p.result(map[string]any{"path": path}, path)
}

func configValidate(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	if len(cfg.MissingEnv) > 0 {
		return fmt.Errorf("config structurally valid but %d referenced env var(s) unset: %s",
			len(cfg.MissingEnv), strings.Join(cfg.MissingEnv, ", "))
	}
	return p.result(
		map[string]any{"valid": true, "accounts": cfg.AccountNames(), "defaultAccount": cfg.Defaults.Account},
		fmt.Sprintf("config valid — %d account(s): %s", len(cfg.Accounts), strings.Join(cfg.AccountNames(), ", ")),
	)
}

// ---- account ------------------------------------------------------------

func accountCommand() *cli.Command {
	return &cli.Command{
		Name:  "account",
		Usage: "inspect and test configured accounts",
		Commands: []*cli.Command{
			{Name: "list", Usage: "list accounts (secrets redacted)", Action: accountList},
			{Name: "show", Usage: "show one account (secrets redacted)", Action: accountShow},
			{Name: "test", Usage: "test outbound + IMAP connectivity", Action: accountTest},
		},
	}
}

type accountView struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	Outbound string `json:"outbound"`
	IMAP     string `json:"imap,omitempty"`
	Default  bool   `json:"default"`
}

func viewAccount(cfg *config.Config, acc *config.Account) accountView {
	v := accountView{Name: acc.Name, From: acc.From, Default: acc.Name == cfg.Defaults.Account}
	if acc.Outbound != nil {
		switch acc.Outbound.Type {
		case config.OutboundSMTP:
			v.Outbound = fmt.Sprintf("smtp %s:%d", acc.Outbound.Host, acc.Outbound.Port)
		case config.OutboundResend:
			v.Outbound = "resend"
		default:
			v.Outbound = acc.Outbound.Type
		}
	}
	if acc.IMAP != nil {
		v.IMAP = fmt.Sprintf("%s:%d", acc.IMAP.Host, acc.IMAP.Port)
	}
	return v
}

func accountList(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	views := make([]accountView, 0, len(cfg.Accounts))
	var b strings.Builder
	for _, name := range cfg.AccountNames() {
		v := viewAccount(cfg, cfg.Accounts[name])
		views = append(views, v)
		marker := " "
		if v.Default {
			marker = "*"
		}
		imap := v.IMAP
		if imap == "" {
			imap = "(send-only)"
		}
		fmt.Fprintf(&b, "%s %-12s %-24s out=%s imap=%s\n", marker, v.Name, v.From, v.Outbound, imap)
	}
	return p.result(views, strings.TrimRight(b.String(), "\n"))
}

func accountShow(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	name := cmd.Args().First()
	if name == "" {
		name = cmd.String("account")
	}
	acc, err := cfg.Resolve(name)
	if err != nil {
		return err
	}
	v := viewAccount(cfg, acc)
	return p.result(v, fmt.Sprintf("%s\n  from:     %s\n  outbound: %s\n  imap:     %s\n  default:  %v",
		v.Name, v.From, v.Outbound, firstNonEmpty(v.IMAP, "(send-only)"), v.Default))
}

func accountTest(ctx context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	acc, err := resolveAccount(cmd, cfg)
	if err != nil {
		return err
	}

	outbound := map[string]any{"ok": true}
	sender, serr := transport.ForAccount(acc)
	if serr != nil {
		outbound = map[string]any{"ok": false, "error": serr.Error()}
	} else if v, ok := sender.(transport.Verifier); ok {
		if err := v.Verify(ctx); err != nil {
			outbound = map[string]any{"ok": false, "error": err.Error()}
		}
	} else {
		outbound["note"] = "no verification available for this transport"
	}

	var imapRes map[string]any
	if acc.IMAP != nil {
		if c, err := imapclient.Connect(acc.IMAP); err != nil {
			imapRes = map[string]any{"ok": false, "error": err.Error()}
		} else {
			_ = c.Close()
			imapRes = map[string]any{"ok": true}
		}
	} else {
		imapRes = map[string]any{"ok": false, "note": "not configured"}
	}

	res := map[string]any{"account": acc.Name, "outbound": outbound, "imap": imapRes}
	text := fmt.Sprintf("account %s\n  outbound: %s\n  imap:     %s",
		acc.Name, okText(outbound), okText(imapRes))
	return p.result(res, text)
}

func okText(m map[string]any) string {
	if ok, _ := m["ok"].(bool); ok {
		return "OK"
	}
	if e, ok := m["error"].(string); ok {
		return "FAIL: " + e
	}
	if n, ok := m["note"].(string); ok {
		return n
	}
	return "FAIL"
}
