package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mxcd/mailotron/internal/imapclient"
)

// RestoreOptions configures a restore run.
type RestoreOptions struct {
	InDir   string
	Folders []string // empty = every folder in the manifest
	Prefix  string   // prepended to each folder name, e.g. "Restored/"
	DryRun  bool
}

// RestoreResult summarizes a restore run.
type RestoreResult struct {
	Folders  int             `json:"folders"`
	Restored int             `json:"restored"`
	Skipped  int             `json:"skipped"` // already present (Message-ID match)
	DryRun   bool            `json:"dryRun,omitempty"`
	Targets  []RestoreTarget `json:"targets,omitempty"`
}

// RestoreTarget reports the per-folder outcome of a restore.
type RestoreTarget struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Restored int    `json:"restored"`
	Skipped  int    `json:"skipped"`
}

// RestoreRun appends the messages from a backup directory back into the mailbox.
// Restore is idempotent: messages whose Message-ID already exists in the target
// folder are skipped, so re-running never creates duplicates.
func RestoreRun(c *imapclient.Client, opts RestoreOptions) (*RestoreResult, error) {
	manifest, err := readManifest(filepath.Join(opts.InDir, manifestFile))
	if err != nil {
		return nil, fmt.Errorf("read backup manifest: %w", err)
	}
	if manifest.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("backup format version %d is newer than supported %d", manifest.FormatVersion, FormatVersion)
	}
	want := nameSet(opts.Folders)

	res := &RestoreResult{DryRun: opts.DryRun}
	for _, ref := range manifest.Folders {
		if len(want) > 0 && !want[ref.Name] {
			continue
		}
		target := opts.Prefix + ref.Name
		dir := filepath.Join(opts.InDir, foldersDir, ref.Dir)
		idx, err := readFolderIndex(filepath.Join(dir, indexFile))
		if err != nil {
			return nil, fmt.Errorf("read index for %q: %w", ref.Name, err)
		}

		existing := map[string]uint32{}
		if !opts.DryRun {
			if err := c.EnsureFolder(target); err != nil {
				return nil, fmt.Errorf("ensure folder %q: %w", target, err)
			}
		}
		existing, err = c.MessageIDIndex(target)
		if err != nil {
			return nil, err
		}

		t := RestoreTarget{Source: ref.Name, Target: target}
		for _, e := range idx.Messages {
			if e.MessageID != "" {
				if _, ok := existing[e.MessageID]; ok {
					t.Skipped++
					continue
				}
			}
			if opts.DryRun {
				t.Restored++
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.File))
			if err != nil {
				return nil, fmt.Errorf("read %q: %w", e.File, err)
			}
			when := parseDate(e.InternalDate)
			if err := c.AppendMessage(target, data, e.Flags, when); err != nil {
				return nil, err
			}
			if e.MessageID != "" {
				existing[e.MessageID] = 0 // guard against duplicate Message-IDs within the backup
			}
			t.Restored++
		}
		res.Folders++
		res.Restored += t.Restored
		res.Skipped += t.Skipped
		res.Targets = append(res.Targets, t)
	}
	return res, nil
}

func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func readManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
