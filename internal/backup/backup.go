package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/imapclient"
)

// Options configures a backup run.
type Options struct {
	OutDir  string
	Folders []string // empty = every selectable folder
	Mirror  bool     // delete local copies of messages/folders gone from the server
	Now     time.Time
}

// Result summarizes a backup run.
type Result struct {
	Dir           string   `json:"dir"`
	Folders       int      `json:"folders"`
	Messages      int      `json:"messages"`         // total messages in the backup
	Downloaded    int      `json:"downloaded"`       // bodies fetched this run
	Pruned        int      `json:"pruned,omitempty"` // local files removed (--mirror)
	PrunedFolders int      `json:"prunedFolders,omitempty"`
	Mirror        bool     `json:"mirror"`
	FolderNames   []string `json:"folderNames,omitempty"`
}

// Run mirrors the selected folders of acc's mailbox into opts.OutDir.
func Run(c *imapclient.Client, acc *config.Account, opts Options) (*Result, error) {
	if opts.OutDir == "" {
		return nil, fmt.Errorf("output directory is required")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	all, err := c.ListFolders()
	if err != nil {
		return nil, err
	}
	want := nameSet(opts.Folders)
	folders := make([]imapclient.Mailbox, 0, len(all))
	for _, f := range all {
		if hasAttr(f.Attributes, `\Noselect`) {
			continue
		}
		if len(want) > 0 && !want[f.Name] {
			continue
		}
		folders = append(folders, f)
	}
	if len(want) > 0 && len(folders) == 0 {
		return nil, fmt.Errorf("none of the requested folders exist or are selectable")
	}

	if err := os.MkdirAll(filepath.Join(opts.OutDir, foldersDir), 0o755); err != nil {
		return nil, err
	}

	res := &Result{Dir: opts.OutDir, Mirror: opts.Mirror}
	manifest := Manifest{
		FormatVersion: FormatVersion,
		Account:       acc.Name,
		CreatedAt:     now.Format(time.RFC3339),
	}
	if acc.IMAP != nil {
		manifest.Server = acc.IMAP.Host
		manifest.Username = acc.IMAP.Username
	}

	keepDirs := map[string]bool{}
	for _, f := range folders {
		idx, downloaded, pruned, err := backupFolder(c, opts, f)
		if err != nil {
			return nil, err
		}
		dir := encodeFolder(f.Name)
		keepDirs[dir] = true
		res.Folders++
		res.Messages += len(idx.Messages)
		res.Downloaded += downloaded
		res.Pruned += pruned
		res.FolderNames = append(res.FolderNames, f.Name)
		manifest.Folders = append(manifest.Folders, FolderRef{
			Name:         f.Name,
			Dir:          dir,
			UIDValidity:  idx.UIDValidity,
			MessageCount: len(idx.Messages),
		})
	}
	manifest.MessageCount = res.Messages

	// Folder-level pruning only happens on a full-mailbox mirror. When the run is
	// scoped with --folder, folders outside the filter must be left untouched
	// rather than deleted just for being out of scope.
	if opts.Mirror && len(opts.Folders) == 0 {
		n, err := pruneFolders(opts.OutDir, keepDirs)
		if err != nil {
			return nil, err
		}
		res.PrunedFolders = n
	}

	if err := writeJSON(filepath.Join(opts.OutDir, manifestFile), manifest); err != nil {
		return nil, err
	}
	return res, nil
}

func backupFolder(c *imapclient.Client, opts Options, mb imapclient.Mailbox) (FolderIndex, int, int, error) {
	uidValidity, uids, err := c.SelectInfo(mb.Name)
	if err != nil {
		return FolderIndex{}, 0, 0, err
	}
	dir := filepath.Join(opts.OutDir, foldersDir, encodeFolder(mb.Name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return FolderIndex{}, 0, 0, err
	}

	prev, _ := readFolderIndex(filepath.Join(dir, indexFile))
	reset := prev == nil || prev.UIDValidity != uidValidity
	if reset {
		if err := removeMessageFiles(dir); err != nil {
			return FolderIndex{}, 0, 0, err
		}
	}

	metas, err := c.FetchMeta(mb.Name, uids)
	if err != nil {
		return FolderIndex{}, 0, 0, err
	}

	// Download bodies for any message whose .eml is not already on disk.
	var missing []uint32
	for _, u := range uids {
		if _, err := os.Stat(filepath.Join(dir, messageFile(u))); os.IsNotExist(err) {
			missing = append(missing, u)
		}
	}
	downloaded := 0
	if err := c.FetchRaw(mb.Name, missing, func(uid uint32, data []byte) error {
		if err := os.WriteFile(filepath.Join(dir, messageFile(uid)), data, 0o644); err != nil {
			return err
		}
		downloaded++
		return nil
	}); err != nil {
		return FolderIndex{}, 0, 0, err
	}

	current := map[uint32]IndexEntry{}
	for _, m := range metas {
		current[m.UID] = IndexEntry{
			UID:          m.UID,
			File:         messageFile(m.UID),
			MessageID:    m.MessageID,
			Flags:        m.Flags,
			InternalDate: formatDate(m.InternalDate),
			Size:         m.Size,
		}
	}

	idx := FolderIndex{Name: mb.Name, UIDValidity: uidValidity, Attributes: mb.Attributes}
	pruned := 0
	if opts.Mirror || reset {
		// Mirror: the backup holds exactly the messages currently on the server.
		for _, u := range uids {
			idx.Messages = append(idx.Messages, current[u])
		}
		if !reset {
			pruned, err = pruneMessages(dir, uids)
			if err != nil {
				return FolderIndex{}, 0, 0, err
			}
		}
	} else {
		// Additive: union of previously-backed-up and current messages. Vanished
		// messages keep their entry (and their .eml) so they survive deletion.
		merged := map[uint32]IndexEntry{}
		if prev != nil {
			for _, e := range prev.Messages {
				merged[e.UID] = e
			}
		}
		for u, e := range current {
			merged[u] = e // refresh flags/metadata for live messages
		}
		uidsOut := make([]uint32, 0, len(merged))
		for u := range merged {
			uidsOut = append(uidsOut, u)
		}
		sort.Slice(uidsOut, func(i, j int) bool { return uidsOut[i] < uidsOut[j] })
		for _, u := range uidsOut {
			idx.Messages = append(idx.Messages, merged[u])
		}
	}

	if err := writeJSON(filepath.Join(dir, indexFile), idx); err != nil {
		return FolderIndex{}, 0, 0, err
	}
	return idx, downloaded, pruned, nil
}

func messageFile(uid uint32) string { return fmt.Sprintf("%d%s", uid, messageExt) }

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func nameSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func hasAttr(attrs []string, want string) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}

// pruneMessages removes .eml files in dir whose UID is not in keep.
func pruneMessages(dir string, keep []uint32) (int, error) {
	want := map[string]bool{}
	for _, u := range keep {
		want[messageFile(u)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != messageExt || want[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func removeMessageFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == messageExt {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// pruneFolders removes backed-up folder directories not present in keep.
func pruneFolders(outDir string, keep map[string]bool) (int, error) {
	root := filepath.Join(outDir, foldersDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func readFolderIndex(path string) (*FolderIndex, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx FolderIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
