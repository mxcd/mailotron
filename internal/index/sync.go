package index

import (
	"sort"
	"strings"

	"github.com/mxcd/mailotron/internal/imapclient"
)

// fetchPrefix bounds the bytes read per new message (headers + text parts).
const fetchPrefix = 256 << 10

// SyncStats reports what one sync run changed.
type SyncStats struct {
	Folders       int `json:"folders"`
	Added         int `json:"added"`
	Removed       int `json:"removed"`
	FoldersReset  int `json:"foldersReset"`
	FoldersPruned int `json:"foldersPruned"`
}

// Sync brings the index in line with the mailbox, incrementally: per folder it
// compares the server's UIDs with the indexed ones, fetches and parses only new
// messages, drops vanished ones and refreshes flags. A changed UIDVALIDITY
// re-indexes that folder. Folders matching exclude are skipped and, like
// folders deleted on the server, dropped from the index. progress (optional) is
// called after each new message.
//
// ponytail: every run enumerates all UIDs and flags of every folder (cheap
// metadata, but O(mailbox)); switch to CONDSTORE/QRESYNC MODSEQ deltas if runs
// on very large mailboxes get slow. Only the first fetchPrefix bytes of a new
// message are read; a text part placed after a huge attachment is missed
// (rare), fetch the text sections via BODYSTRUCTURE if that ever matters.
func Sync(c *imapclient.Client, x *Index, exclude []string, progress func(folder string, uid uint32)) (*SyncStats, error) {
	folders, err := c.ListFolders()
	if err != nil {
		return nil, err
	}
	st := &SyncStats{}
	live := map[string]bool{}
	for _, f := range folders {
		if hasAttr(f.Attributes, `\Noselect`) || hasAttr(f.Attributes, `\NonExistent`) || Excluded(f, exclude) {
			continue
		}
		live[f.Name] = true
		if err := syncFolder(c, x, f.Name, st, progress); err != nil {
			return st, err
		}
		st.Folders++
	}
	pruned, err := x.PruneFolders(live)
	st.FoldersPruned = pruned
	return st, err
}

func syncFolder(c *imapclient.Client, x *Index, folder string, st *SyncStats, progress func(string, uint32)) error {
	uidValidity, uids, err := c.SelectInfo(folder)
	if err != nil {
		return err
	}
	prevValidity, indexed, found, err := x.FolderState(folder)
	if err != nil {
		return err
	}
	if !found || prevValidity != uidValidity {
		if found {
			st.FoldersReset++
		}
		if err := x.ResetFolder(folder, uidValidity); err != nil {
			return err
		}
		indexed = map[uint32]bool{}
	}

	onServer := make(map[uint32]bool, len(uids))
	var missing []uint32
	for _, u := range uids {
		onServer[u] = true
		if !indexed[u] {
			missing = append(missing, u)
		}
	}
	var gone []uint32
	for u := range indexed {
		if !onServer[u] {
			gone = append(gone, u)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i] < gone[j] })
	if err := x.Delete(folder, gone); err != nil {
		return err
	}
	st.Removed += len(gone)

	metas, err := c.FetchMeta(folder, uids)
	if err != nil {
		return err
	}
	byUID := make(map[uint32]imapclient.MessageMeta, len(metas))
	flags := map[uint32][]string{}
	for _, m := range metas {
		byUID[m.UID] = m
		if indexed[m.UID] {
			flags[m.UID] = m.Flags
		}
	}
	if err := x.SetFlags(folder, flags); err != nil {
		return err
	}

	// Small messages are fetched whole; only large ones (attachments) are cut to
	// a prefix. Besides saving a partial FETCH where it buys nothing, this keeps
	// the common path off partial responses, which some servers (GreenMail)
	// encode in a way go-imap cannot parse.
	var small, large []uint32
	for _, u := range missing {
		if byUID[u].Size > fetchPrefix {
			large = append(large, u)
		} else {
			small = append(small, u)
		}
	}
	add := func(uid uint32, raw []byte) error {
		d := ParseDoc(raw)
		d.UID = uid
		if m, ok := byUID[uid]; ok {
			d.Flags, d.Size = m.Flags, m.Size
			if d.Date.IsZero() {
				d.Date = m.InternalDate
			}
			if d.MessageID == "" {
				d.MessageID = m.MessageID
			}
		}
		if err := x.Put(folder, d); err != nil {
			return err
		}
		st.Added++
		if progress != nil {
			progress(folder, uid)
		}
		return nil
	}
	if err := c.FetchRaw(folder, small, add); err != nil {
		return err
	}
	return c.FetchPrefix(folder, large, fetchPrefix, add)
}

func hasAttr(attrs []string, want string) bool {
	for _, a := range attrs {
		if strings.EqualFold(a, want) {
			return true
		}
	}
	return false
}

// Excluded reports whether folder matches one of the exclude entries by full
// name or by its last path segment (split on the server's delimiter).
func Excluded(folder imapclient.Mailbox, exclude []string) bool {
	leaf := folder.Name
	if folder.Delimiter != "" {
		if i := strings.LastIndex(folder.Name, folder.Delimiter); i >= 0 {
			leaf = folder.Name[i+len(folder.Delimiter):]
		}
	}
	for _, e := range exclude {
		if strings.EqualFold(e, folder.Name) || strings.EqualFold(e, leaf) {
			return true
		}
	}
	return false
}
