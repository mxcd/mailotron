package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/mxcd/mailotron/internal/imapclient"
	cli "github.com/urfave/cli/v3"
)

// ---- folder -------------------------------------------------------------

func folderCommand() *cli.Command {
	return &cli.Command{
		Name:  "folder",
		Usage: "manage IMAP folders (directories)",
		Commands: []*cli.Command{
			{Name: "list", Usage: "list folders", Action: folderList},
			{Name: "create", Usage: "create a folder: <name>", Action: folderCreate},
			{Name: "rename", Usage: "rename a folder: <old> <new>", Action: folderRename},
			{Name: "delete", Usage: "delete a folder: <name>", Action: folderDelete},
		},
	}
}

func folderList(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	c, _, err := connectIMAP(cmd, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	folders, err := c.ListFolders()
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, f := range folders {
		fmt.Fprintln(&b, f.Name)
	}
	return p.result(map[string]any{"folders": folders}, strings.TrimRight(b.String(), "\n"))
}

func folderCreate(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("folder name required")
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.CreateFolder(name); err != nil {
		return err
	}
	return p.result(map[string]any{"created": name}, "created folder "+name)
}

func folderRename(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	oldName, newName := cmd.Args().Get(0), cmd.Args().Get(1)
	if oldName == "" || newName == "" {
		return usageErr("usage: folder rename <old> <new>")
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.RenameFolder(oldName, newName); err != nil {
		return err
	}
	return p.result(map[string]any{"renamed": oldName, "to": newName}, fmt.Sprintf("renamed %s -> %s", oldName, newName))
}

func folderDelete(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	name := cmd.Args().First()
	if name == "" {
		return usageErr("folder name required")
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.DeleteFolder(name); err != nil {
		return err
	}
	return p.result(map[string]any{"deleted": name}, "deleted folder "+name)
}

// ---- message ------------------------------------------------------------

func folderFlag() cli.Flag {
	return &cli.StringFlag{Name: "folder", Value: "INBOX", Usage: "IMAP folder"}
}

func listFilterFlags() []cli.Flag {
	return []cli.Flag{
		folderFlag(),
		&cli.BoolFlag{Name: "unseen", Usage: "only unseen messages"},
		&cli.BoolFlag{Name: "flagged", Usage: "only flagged messages"},
		&cli.StringFlag{Name: "since", Usage: "on/after date YYYY-MM-DD"},
		&cli.StringFlag{Name: "before", Usage: "before date YYYY-MM-DD"},
		&cli.StringFlag{Name: "from", Usage: "From header contains"},
		&cli.StringFlag{Name: "to", Usage: "To header contains"},
		&cli.StringFlag{Name: "subject", Usage: "Subject contains"},
		&cli.StringFlag{Name: "text", Usage: "full-text contains"},
		&cli.IntFlag{Name: "limit", Value: 50, Usage: "max messages to return"},
	}
}

func messageCommand() *cli.Command {
	return &cli.Command{
		Name:  "message",
		Usage: "list, read and organize messages",
		Commands: []*cli.Command{
			{Name: "list", Usage: "list messages with optional filters", Flags: listFilterFlags(), Action: messageList},
			{Name: "search", Usage: "alias for list (server-side IMAP search)", Flags: listFilterFlags(), Action: messageList},
			{
				Name:   "show",
				Usage:  "show one message: <uid>",
				Flags:  []cli.Flag{folderFlag(), &cli.BoolFlag{Name: "no-body", Usage: "headers only"}},
				Action: messageShow,
			},
			{
				Name:   "move",
				Usage:  "move a message: <uid> --to-folder <f>",
				Flags:  []cli.Flag{folderFlag(), &cli.StringFlag{Name: "to-folder", Usage: "destination folder"}},
				Action: messageMove,
			},
			{
				Name:   "copy",
				Usage:  "copy a message: <uid> --to-folder <f>",
				Flags:  []cli.Flag{folderFlag(), &cli.StringFlag{Name: "to-folder", Usage: "destination folder"}},
				Action: messageCopy,
			},
			{
				Name:  "flag",
				Usage: "set/clear flags: <uid> --seen|--unseen|--flagged|--unflagged",
				Flags: []cli.Flag{
					folderFlag(),
					&cli.BoolFlag{Name: "seen"}, &cli.BoolFlag{Name: "unseen"},
					&cli.BoolFlag{Name: "flagged"}, &cli.BoolFlag{Name: "unflagged"},
				},
				Action: messageFlag,
			},
			{
				Name:   "delete",
				Usage:  "delete a message: <uid> (moves to Trash; --hard expunges)",
				Flags:  []cli.Flag{folderFlag(), &cli.BoolFlag{Name: "hard", Usage: "mark \\Deleted and expunge"}},
				Action: messageDelete,
			},
			{
				Name:  "download",
				Usage: "download attachments (and body): <uid>",
				Flags: []cli.Flag{
					folderFlag(),
					&cli.StringFlag{Name: "out", Value: ".", Usage: "output directory"},
					&cli.BoolFlag{Name: "attachments-only", Usage: "do not write body files"},
				},
				Action: messageDownload,
			},
		},
	}
}

func messageList(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()

	folder := cmd.String("folder")
	q := imapclient.SearchQuery{
		Unseen:  cmd.Bool("unseen"),
		Flagged: cmd.Bool("flagged"),
		Since:   cmd.String("since"),
		Before:  cmd.String("before"),
		From:    cmd.String("from"),
		To:      cmd.String("to"),
		Subject: cmd.String("subject"),
		Text:    cmd.String("text"),
		Limit:   int(cmd.Int("limit")),
	}
	msgs, err := c.ListMessages(folder, q)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, m := range msgs {
		state := "•"
		if m.Seen {
			state = " "
		}
		fmt.Fprintf(&b, "%s %7d  %-24s  %s\n", state, m.UID, truncate(strings.Join(m.From, ","), 24), m.Subject)
	}
	return p.result(
		map[string]any{"folder": folder, "count": len(msgs), "messages": msgs},
		strings.TrimRight(b.String(), "\n"),
	)
}

func messageShow(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	uid, err := parseUID(cmd.Args().First())
	if err != nil {
		return err
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	detail, err := c.ShowMessage(cmd.String("folder"), uid, !cmd.Bool("no-body"))
	if err != nil {
		return err
	}
	if p.isJSON() {
		return p.emitJSON(detail)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "UID:     %d\nFrom:    %s\nTo:      %s\nDate:    %s\nSubject: %s\nFlags:   %s\n",
		detail.UID, strings.Join(detail.From, ", "), strings.Join(detail.To, ", "),
		detail.Date, detail.Subject, strings.Join(detail.Flags, " "))
	for _, a := range detail.Attachments {
		fmt.Fprintf(&b, "Attachment: %s (%s, %d bytes)\n", a.Filename, a.ContentType, a.Size)
	}
	if detail.Text != "" {
		fmt.Fprintf(&b, "\n%s\n", detail.Text)
	} else if detail.HTML != "" {
		fmt.Fprintf(&b, "\n[HTML only — %d bytes]\n", len(detail.HTML))
	}
	return p.result(detail, strings.TrimRight(b.String(), "\n"))
}

func messageMove(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	uid, err := parseUID(cmd.Args().First())
	if err != nil {
		return err
	}
	dest := cmd.String("to-folder")
	if dest == "" {
		return usageErr("--to-folder is required")
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	folder := cmd.String("folder")
	if err := c.MoveMessage(folder, uid, dest); err != nil {
		return err
	}
	return p.result(map[string]any{"moved": uid, "from": folder, "to": dest}, fmt.Sprintf("moved %d -> %s", uid, dest))
}

func messageCopy(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	uid, err := parseUID(cmd.Args().First())
	if err != nil {
		return err
	}
	dest := cmd.String("to-folder")
	if dest == "" {
		return usageErr("--to-folder is required")
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	folder := cmd.String("folder")
	if err := c.CopyMessage(folder, uid, dest); err != nil {
		return err
	}
	return p.result(map[string]any{"copied": uid, "from": folder, "to": dest}, fmt.Sprintf("copied %d -> %s", uid, dest))
}

func messageFlag(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	uid, err := parseUID(cmd.Args().First())
	if err != nil {
		return err
	}

	var flag imap.Flag
	var add, set bool
	switch {
	case cmd.Bool("seen"):
		flag, add, set = imap.FlagSeen, true, true
	case cmd.Bool("unseen"):
		flag, add, set = imap.FlagSeen, false, true
	case cmd.Bool("flagged"):
		flag, add, set = imap.FlagFlagged, true, true
	case cmd.Bool("unflagged"):
		flag, add, set = imap.FlagFlagged, false, true
	}
	if !set {
		return usageErr("one of --seen/--unseen/--flagged/--unflagged is required")
	}

	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.FlagMessage(cmd.String("folder"), uid, flag, add); err != nil {
		return err
	}
	return p.result(
		map[string]any{"uid": uid, "flag": string(flag), "added": add},
		fmt.Sprintf("uid %d: %s %s", uid, map[bool]string{true: "+", false: "-"}[add], flag),
	)
}

func messageDelete(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	uid, err := parseUID(cmd.Args().First())
	if err != nil {
		return err
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	hard := cmd.Bool("hard")
	if err := c.DeleteMessage(cmd.String("folder"), uid, hard); err != nil {
		return err
	}
	mode := "trashed"
	if hard {
		mode = "expunged"
	}
	return p.result(map[string]any{"uid": uid, "hard": hard, "result": mode}, fmt.Sprintf("%s message %d", mode, uid))
}

func messageDownload(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	uid, err := parseUID(cmd.Args().First())
	if err != nil {
		return err
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	folder := cmd.String("folder")
	outDir := cmd.String("out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	atts, err := c.FetchAttachments(folder, uid)
	if err != nil {
		return err
	}
	var written []string
	for i, a := range atts {
		name := safeFilename(a.Filename)
		if name == "" {
			name = fmt.Sprintf("attachment-%d-%d", uid, i)
		}
		path := filepath.Join(outDir, name)
		if err := os.WriteFile(path, a.Data, 0o644); err != nil {
			return err
		}
		written = append(written, path)
	}

	if !cmd.Bool("attachments-only") {
		detail, err := c.ShowMessage(folder, uid, true)
		if err != nil {
			return err
		}
		if detail.HTML != "" {
			path := filepath.Join(outDir, fmt.Sprintf("%d.html", uid))
			if err := os.WriteFile(path, []byte(detail.HTML), 0o644); err != nil {
				return err
			}
			written = append(written, path)
		}
		if detail.Text != "" {
			path := filepath.Join(outDir, fmt.Sprintf("%d.txt", uid))
			if err := os.WriteFile(path, []byte(detail.Text), 0o644); err != nil {
				return err
			}
			written = append(written, path)
		}
	}

	return p.result(
		map[string]any{"uid": uid, "files": written, "attachments": len(atts)},
		fmt.Sprintf("downloaded %d file(s) to %s", len(written), outDir),
	)
}

// connectFor loads config and opens an IMAP connection for the selected account.
func connectFor(cmd *cli.Command) (*imapclient.Client, error) {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return nil, err
	}
	c, _, err := connectIMAP(cmd, cfg)
	return c, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func safeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "." || name == ".." {
		return ""
	}
	return name
}
