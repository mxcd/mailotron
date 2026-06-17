package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mxcd/mailotron/internal/backup"
	cli "github.com/urfave/cli/v3"
)

func backupCommand() *cli.Command {
	return &cli.Command{
		Name:  "backup",
		Usage: "back up an entire mailbox to a directory of files (restic-friendly)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Aliases: []string{"d"}, Usage: "output directory (required)"},
			&cli.StringSliceFlag{Name: "folder", Usage: "limit to folder(s); repeatable (default: all)"},
			&cli.BoolFlag{Name: "mirror", Usage: "delete local copies of messages/folders no longer on the server"},
		},
		Action: backupRun,
	}
}

func restoreCommand() *cli.Command {
	return &cli.Command{
		Name:  "restore",
		Usage: "restore a mailbox backup directory back into IMAP",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "in", Aliases: []string{"d"}, Usage: "backup directory (required)"},
			&cli.StringSliceFlag{Name: "folder", Usage: "limit to folder(s); repeatable (default: all)"},
			&cli.StringFlag{Name: "prefix", Usage: "prepend to each folder name, e.g. \"Restored/\""},
			&cli.BoolFlag{Name: "dry-run", Usage: "report what would be restored without appending"},
		},
		Action: restoreRun,
	}
}

func backupRun(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	out := cmd.String("out")
	if out == "" {
		return usageErr("--out <dir> is required")
	}
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	c, acc, err := connectIMAP(cmd, cfg)
	if err != nil {
		return err
	}
	defer c.Close()

	res, err := backup.Run(c, acc, backup.Options{
		OutDir:  out,
		Folders: cmd.StringSlice("folder"),
		Mirror:  cmd.Bool("mirror"),
	})
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "backed up %d message(s) across %d folder(s) to %s\n",
		res.Messages, res.Folders, res.Dir)
	fmt.Fprintf(&b, "%d newly downloaded this run", res.Downloaded)
	if res.Mirror {
		fmt.Fprintf(&b, "; pruned %d message(s), %d folder(s)", res.Pruned, res.PrunedFolders)
	}
	fmt.Fprintf(&b, "\n\narchive to S3 with restic:\n"+
		"  export RESTIC_PASSWORD=… AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=…\n"+
		"  restic -r s3:s3.amazonaws.com/your-bucket/mailbox backup %s", res.Dir)
	return p.result(res, b.String())
}

func restoreRun(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	in := cmd.String("in")
	if in == "" {
		return usageErr("--in <dir> is required")
	}
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()

	res, err := backup.RestoreRun(c, backup.RestoreOptions{
		InDir:   in,
		Folders: cmd.StringSlice("folder"),
		Prefix:  cmd.String("prefix"),
		DryRun:  cmd.Bool("dry-run"),
	})
	if err != nil {
		return err
	}

	verb := "restored"
	if res.DryRun {
		verb = "would restore"
	}
	text := fmt.Sprintf("%s %d message(s) into %d folder(s); %d already present (skipped)",
		verb, res.Restored, res.Folders, res.Skipped)
	return p.result(res, text)
}
