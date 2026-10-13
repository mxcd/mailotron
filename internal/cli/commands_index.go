package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mxcd/mailotron/internal/index"
	cli "github.com/urfave/cli/v3"
)

func indexCommand() *cli.Command {
	return &cli.Command{
		Name:  "index",
		Usage: "local full-text index of a mailbox (SQLite FTS5, synced incrementally from IMAP)",
		Commands: []*cli.Command{
			{
				Name:   "sync",
				Usage:  "create or incrementally update the account's index (and embeddings, if index.embedding is configured)",
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "no-embed", Usage: "skip computing embeddings for new messages"}},
				Action: indexSync,
			},
			{
				Name:      "search",
				Usage:     "ranked full-text search in the local index: <words...>",
				ArgsUsage: "<words...>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "folder", Usage: "only this folder"},
					&cli.StringFlag{Name: "since", Usage: "on/after date YYYY-MM-DD"},
					&cli.StringFlag{Name: "before", Usage: "before date YYYY-MM-DD"},
					&cli.IntFlag{Name: "limit", Value: 20, Usage: "max results"},
					&cli.BoolFlag{Name: "raw", Usage: "pass the query to FTS5 unchanged (AND/OR/NOT, NEAR, subject:word)"},
					&cli.StringFlag{Name: "mode", Usage: "fts | semantic | hybrid (default: hybrid when embeddings exist, else fts)"},
				},
				Action: indexSearch,
			},
			{Name: "status", Usage: "show index size and freshness", Action: indexStatus},
		},
	}
}

// openIndex opens the selected account's index; the embedder is nil when no
// index.embedding is configured.
func openIndex(cmd *cli.Command) (*index.Index, string, *index.Embedder, []string, error) {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return nil, "", nil, nil, err
	}
	acc, err := resolveAccount(cmd, cfg)
	if err != nil {
		return nil, "", nil, nil, err
	}
	path, err := index.PathFor(acc.Name)
	if err != nil {
		return nil, "", nil, nil, err
	}
	x, err := index.Open(path)
	if err != nil {
		return nil, "", nil, nil, err
	}
	var e *index.Embedder
	if cfg.Index.Embedding != nil {
		e = index.NewEmbedder(*cfg.Index.Embedding)
	}
	return x, acc.Name, e, cfg.Index.ExcludeFolders, nil
}

func indexSync(ctx context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	c, err := connectFor(cmd)
	if err != nil {
		return err
	}
	defer c.Close()
	x, account, emb, exclude, err := openIndex(cmd)
	if err != nil {
		return err
	}
	defer x.Close()

	start := time.Now()
	errw := cmd.Root().ErrWriter
	n := 0
	stats, err := index.Sync(c, x, exclude, func(folder string, _ uint32) {
		n++
		if !p.isJSON() && n%250 == 0 {
			fmt.Fprintf(errw, "indexed %d new messages (now in %s)\n", n, folder)
		}
	})
	if err != nil {
		return err
	}
	embedded := 0
	if emb != nil && !cmd.Bool("no-embed") {
		embedded, err = x.EmbedPending(ctx, emb, func(done int) {
			if !p.isJSON() && done%256 == 0 {
				fmt.Fprintf(errw, "embedded %d messages\n", done)
			}
		})
		if err != nil {
			return fmt.Errorf("full-text index is up to date, but embedding stopped after %d messages (re-run to resume): %w", embedded, err)
		}
	}
	took := time.Since(start).Round(time.Millisecond)
	return p.result(
		map[string]any{"account": account, "stats": stats, "embedded": embedded, "took": took.String()},
		fmt.Sprintf("%s: %d folders, +%d new, -%d removed, %d reset, %d pruned, %d embedded (%s)",
			account, stats.Folders, stats.Added, stats.Removed, stats.FoldersReset, stats.FoldersPruned, embedded, took),
	)
}

func indexSearch(ctx context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	text := strings.Join(cmd.Args().Slice(), " ")
	if strings.TrimSpace(text) == "" {
		return usageErr("search words required")
	}
	q := index.Query{Text: text, Raw: cmd.Bool("raw"), Folder: cmd.String("folder"), Limit: int(cmd.Int("limit"))}
	var err error
	if q.Since, err = parseDay(cmd.String("since"), "--since"); err != nil {
		return err
	}
	if q.Before, err = parseDay(cmd.String("before"), "--before"); err != nil {
		return err
	}
	x, account, emb, _, err := openIndex(cmd)
	if err != nil {
		return err
	}
	defer x.Close()

	q.Mode = cmd.String("mode")
	if emb != nil {
		q.Model = emb.Key()
	}
	if q.Mode == "" {
		q.Mode = index.ModeFTS
		if emb != nil && !q.Raw {
			if st, err := x.Status(q.Model); err == nil && st.Embedded > 0 {
				q.Mode = index.ModeHybrid
			}
		}
	}
	if q.Mode != index.ModeFTS {
		if emb == nil {
			return usageErr("--mode %s needs index.embedding in the config", q.Mode)
		}
		if q.Vector, err = emb.EmbedQuery(ctx, text); err != nil {
			return err
		}
	}
	hits, err := x.Search(q)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "%s  %-20s %7d  %-24s  %s\n    %s\n", h.Date, truncate(h.Folder, 20), h.UID, truncate(h.From, 24), h.Subject, h.Snippet)
	}
	return p.result(
		map[string]any{"account": account, "query": text, "mode": q.Mode, "count": len(hits), "hits": hits},
		strings.TrimRight(b.String(), "\n"),
	)
}

func indexStatus(_ context.Context, cmd *cli.Command) error {
	p := newPrinter(cmd)
	x, account, emb, _, err := openIndex(cmd)
	if err != nil {
		return err
	}
	defer x.Close()
	model := ""
	if emb != nil {
		model = emb.Key()
	}
	st, err := x.Status(model)
	if err != nil {
		return err
	}
	path, _ := index.PathFor(account)
	text := fmt.Sprintf("%s: %d messages in %d folders, newest %s (%s)", account, st.Messages, st.Folders, st.Newest, path)
	if model != "" {
		text += fmt.Sprintf("\nembeddings: %d/%d with %s", st.Embedded, st.Messages, model)
	}
	return p.result(map[string]any{"account": account, "path": path, "model": model, "status": st}, text)
}

func parseDay(s, flag string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, usageErr("invalid %s date %q (want YYYY-MM-DD)", flag, s)
	}
	return t, nil
}
