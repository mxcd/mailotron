package imapclient

import (
	"fmt"
	"sort"
	"time"

	"github.com/emersion/go-imap/v2"
)

// fetchChunk bounds how many messages are fetched (and buffered) per round-trip
// so that backing up a large folder does not hold the whole mailbox in memory.
const fetchChunk = 50

// MessageMeta is the envelope-level metadata kept for each backed-up message.
type MessageMeta struct {
	UID          uint32
	MessageID    string
	Flags        []string
	InternalDate time.Time
	Size         int64
}

// SelectInfo opens a folder read-only and returns its UIDVALIDITY plus all
// contained UIDs (sorted ascending).
func (c *Client) SelectInfo(folder string) (uidValidity uint32, uids []uint32, err error) {
	sel, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return 0, nil, fmt.Errorf("select %q: %w", folder, err)
	}
	uidValidity = sel.UIDValidity
	if sel.NumMessages == 0 {
		return uidValidity, nil, nil
	}
	var ss imap.SeqSet
	ss.AddRange(1, sel.NumMessages)
	bufs, err := c.c.Fetch(ss, &imap.FetchOptions{UID: true}).Collect()
	if err != nil {
		return 0, nil, fmt.Errorf("enumerate uids in %q: %w", folder, err)
	}
	uids = make([]uint32, 0, len(bufs))
	for _, b := range bufs {
		uids = append(uids, uint32(b.UID))
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	return uidValidity, uids, nil
}

// FetchMeta returns envelope-level metadata for the given UIDs. The caller must
// have selected the folder context via a prior SelectInfo on the same folder;
// FetchMeta re-selects defensively.
func (c *Client) FetchMeta(folder string, uids []uint32) ([]MessageMeta, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	if _, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}
	opts := &imap.FetchOptions{UID: true, Flags: true, InternalDate: true, RFC822Size: true, Envelope: true}
	out := make([]MessageMeta, 0, len(uids))
	for _, chunk := range chunkUIDs(uids, fetchChunk) {
		bufs, err := c.c.Fetch(imap.UIDSetNum(toIMAPUIDs(chunk)...), opts).Collect()
		if err != nil {
			return nil, fmt.Errorf("fetch metadata in %q: %w", folder, err)
		}
		for _, b := range bufs {
			m := MessageMeta{
				UID:          uint32(b.UID),
				Flags:        flagStrings(b.Flags),
				InternalDate: b.InternalDate,
				Size:         b.RFC822Size,
			}
			if b.Envelope != nil {
				m.MessageID = b.Envelope.MessageID
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// FetchRaw streams the verbatim RFC822 bytes of each UID to fn. Bodies are
// fetched with PEEK so the \Seen flag is never altered. Messages are fetched in
// bounded chunks to cap memory use on large folders.
func (c *Client) FetchRaw(folder string, uids []uint32, fn func(uid uint32, data []byte) error) error {
	if len(uids) == 0 {
		return nil
	}
	if _, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", folder, err)
	}
	bs := &imap.FetchItemBodySection{Peek: true}
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{bs}}
	for _, chunk := range chunkUIDs(uids, fetchChunk) {
		bufs, err := c.c.Fetch(imap.UIDSetNum(toIMAPUIDs(chunk)...), opts).Collect()
		if err != nil {
			return fmt.Errorf("fetch bodies in %q: %w", folder, err)
		}
		for _, b := range bufs {
			data := b.FindBodySection(bs)
			if err := fn(uint32(b.UID), data); err != nil {
				return err
			}
		}
	}
	return nil
}

// MessageIDIndex maps each Message-ID present in a folder to its UID. It is used
// to make restore idempotent (skip messages already present). A missing folder
// yields an empty index rather than an error.
func (c *Client) MessageIDIndex(folder string) (map[string]uint32, error) {
	sel, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return map[string]uint32{}, nil
	}
	idx := map[string]uint32{}
	if sel.NumMessages == 0 {
		return idx, nil
	}
	var ss imap.SeqSet
	ss.AddRange(1, sel.NumMessages)
	bufs, err := c.c.Fetch(ss, &imap.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("index message-ids in %q: %w", folder, err)
	}
	for _, b := range bufs {
		if b.Envelope != nil && b.Envelope.MessageID != "" {
			idx[b.Envelope.MessageID] = uint32(b.UID)
		}
	}
	return idx, nil
}

// EnsureFolder creates a folder if it does not already exist. Existing folders
// (including those reported by a server-specific "already exists" error) are
// treated as success.
func (c *Client) EnsureFolder(name string) error {
	folders, err := c.ListFolders()
	if err != nil {
		return err
	}
	for _, f := range folders {
		if f.Name == name {
			return nil
		}
	}
	return c.CreateFolder(name)
}

// AppendMessage uploads a raw RFC822 message into a folder with the given flags
// and internal date. The server-managed \Recent flag is dropped (APPEND must
// not set it).
func (c *Client) AppendMessage(folder string, data []byte, flags []string, internalDate time.Time) error {
	opts := &imap.AppendOptions{Time: internalDate}
	for _, f := range flags {
		// \Recent is server-managed and must not be set by APPEND.
		if f == `\Recent` {
			continue
		}
		opts.Flags = append(opts.Flags, imap.Flag(f))
	}
	cmd := c.c.Append(folder, int64(len(data)), opts)
	if _, err := cmd.Write(data); err != nil {
		_ = cmd.Close()
		return fmt.Errorf("append to %q: %w", folder, err)
	}
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("append to %q: %w", folder, err)
	}
	if _, err := cmd.Wait(); err != nil {
		return fmt.Errorf("append to %q: %w", folder, err)
	}
	return nil
}

func chunkUIDs(uids []uint32, size int) [][]uint32 {
	var out [][]uint32
	for i := 0; i < len(uids); i += size {
		end := i + size
		if end > len(uids) {
			end = len(uids)
		}
		out = append(out, uids[i:end])
	}
	return out
}

func toIMAPUIDs(uids []uint32) []imap.UID {
	out := make([]imap.UID, len(uids))
	for i, u := range uids {
		out[i] = imap.UID(u)
	}
	return out
}
