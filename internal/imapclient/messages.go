package imapclient

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	_ "github.com/emersion/go-message/charset" // register non-UTF8 charset decoders
	"github.com/emersion/go-message/mail"
)

const dateLayout = "2006-01-02"
const displayTime = "02.01.2006 15:04:05"

var summaryFetch = &imap.FetchOptions{
	UID:          true,
	Flags:        true,
	Envelope:     true,
	InternalDate: true,
	RFC822Size:   true,
}

// ListMessages selects a folder and returns message summaries matching the
// query. A zero query lists the most recent messages (bounded by Limit).
func (c *Client) ListMessages(folder string, q SearchQuery) ([]MessageSummary, error) {
	sel, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}

	crit, hasCrit, err := buildCriteria(q)
	if err != nil {
		return nil, err
	}

	var numSet imap.NumSet
	if hasCrit {
		data, err := c.c.UIDSearch(crit, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("search %q: %w", folder, err)
		}
		uids := data.AllUIDs()
		if len(uids) == 0 {
			return []MessageSummary{}, nil
		}
		sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
		if q.Limit > 0 && len(uids) > q.Limit {
			uids = uids[len(uids)-q.Limit:]
		}
		numSet = imap.UIDSetNum(uids...)
	} else {
		if sel.NumMessages == 0 {
			return []MessageSummary{}, nil
		}
		start := uint32(1)
		if q.Limit > 0 && sel.NumMessages > uint32(q.Limit) {
			start = sel.NumMessages - uint32(q.Limit) + 1
		}
		var ss imap.SeqSet
		ss.AddRange(start, sel.NumMessages)
		numSet = ss
	}

	bufs, err := c.c.Fetch(numSet, summaryFetch).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", folder, err)
	}
	out := make([]MessageSummary, 0, len(bufs))
	for _, b := range bufs {
		out = append(out, summaryFromBuf(b, folder, sel.UIDValidity))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

// ShowMessage fetches one message. With includeBody it also decodes the text,
// HTML and attachment metadata. Body fetches use PEEK so the \Seen flag is not
// changed implicitly.
func (c *Client) ShowMessage(folder string, uid uint32, includeBody bool) (*MessageDetail, error) {
	sel, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}

	opts := *summaryFetch
	var bs *imap.FetchItemBodySection
	if includeBody {
		bs = &imap.FetchItemBodySection{Peek: true}
		opts.BodySection = []*imap.FetchItemBodySection{bs}
	}

	bufs, err := c.c.Fetch(imap.UIDSetNum(imap.UID(uid)), &opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch uid %d: %w", uid, err)
	}
	if len(bufs) == 0 {
		return nil, fmt.Errorf("message uid %d not found in %q", uid, folder)
	}
	buf := bufs[0]
	detail := &MessageDetail{MessageSummary: summaryFromBuf(buf, folder, sel.UIDValidity)}

	if includeBody {
		raw := buf.FindBodySection(bs)
		if len(raw) > 0 {
			text, html, atts, err := parseParts(raw, false)
			if err != nil {
				return nil, err
			}
			detail.Text, detail.HTML = text, html
			for _, a := range atts {
				detail.Attachments = append(detail.Attachments, AttachmentInfo{
					Filename: a.Filename, ContentType: a.ContentType, Size: len(a.Data),
				})
			}
		}
	}
	return detail, nil
}

// MoveMessage moves a message to dest, using IMAP MOVE when available and
// falling back to COPY + \Deleted + EXPUNGE otherwise.
func (c *Client) MoveMessage(folder string, uid uint32, dest string) error {
	if _, err := c.c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", folder, err)
	}
	uidSet := imap.UIDSetNum(imap.UID(uid))
	if c.c.Caps().Has(imap.CapMove) {
		if _, err := c.c.Move(uidSet, dest).Wait(); err != nil {
			return fmt.Errorf("move uid %d -> %q: %w", uid, dest, err)
		}
		return nil
	}
	if _, err := c.c.Copy(uidSet, dest).Wait(); err != nil {
		return fmt.Errorf("copy uid %d -> %q: %w", uid, dest, err)
	}
	if err := c.storeFlag(uidSet, imap.FlagDeleted, true); err != nil {
		return err
	}
	if _, err := c.c.Expunge().Collect(); err != nil {
		return fmt.Errorf("expunge: %w", err)
	}
	return nil
}

// CopyMessage copies a message to dest.
func (c *Client) CopyMessage(folder string, uid uint32, dest string) error {
	if _, err := c.c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", folder, err)
	}
	if _, err := c.c.Copy(imap.UIDSetNum(imap.UID(uid)), dest).Wait(); err != nil {
		return fmt.Errorf("copy uid %d -> %q: %w", uid, dest, err)
	}
	return nil
}

// FlagMessage adds or removes a flag on a message.
func (c *Client) FlagMessage(folder string, uid uint32, flag imap.Flag, add bool) error {
	if _, err := c.c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", folder, err)
	}
	return c.storeFlag(imap.UIDSetNum(imap.UID(uid)), flag, add)
}

// DeleteMessage moves a message to Trash (soft) or marks \Deleted and expunges
// it (hard).
func (c *Client) DeleteMessage(folder string, uid uint32, hard bool) error {
	if !hard {
		return c.MoveMessage(folder, uid, c.cfg.TrashOrDefault())
	}
	if _, err := c.c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", folder, err)
	}
	uidSet := imap.UIDSetNum(imap.UID(uid))
	if err := c.storeFlag(uidSet, imap.FlagDeleted, true); err != nil {
		return err
	}
	if _, err := c.c.Expunge().Collect(); err != nil {
		return fmt.Errorf("expunge: %w", err)
	}
	return nil
}

// FetchAttachments returns the decoded attachments of a message.
func (c *Client) FetchAttachments(folder string, uid uint32) ([]Attachment, error) {
	if _, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}
	bs := &imap.FetchItemBodySection{Peek: true}
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{bs}}
	bufs, err := c.c.Fetch(imap.UIDSetNum(imap.UID(uid)), opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch uid %d: %w", uid, err)
	}
	if len(bufs) == 0 {
		return nil, fmt.Errorf("message uid %d not found in %q", uid, folder)
	}
	raw := bufs[0].FindBodySection(bs)
	_, _, atts, err := parseParts(raw, true)
	return atts, err
}

func (c *Client) storeFlag(uidSet imap.UIDSet, flag imap.Flag, add bool) error {
	op := imap.StoreFlagsAdd
	if !add {
		op = imap.StoreFlagsDel
	}
	store := &imap.StoreFlags{Op: op, Flags: []imap.Flag{flag}}
	if _, err := c.c.Store(uidSet, store, nil).Collect(); err != nil {
		return fmt.Errorf("store flag %s: %w", flag, err)
	}
	return nil
}

// Attachment is a decoded attachment payload (used by download).
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

func buildCriteria(q SearchQuery) (*imap.SearchCriteria, bool, error) {
	crit := &imap.SearchCriteria{}
	has := false
	if q.Unseen {
		crit.NotFlag = append(crit.NotFlag, imap.FlagSeen)
		has = true
	}
	if q.Flagged {
		crit.Flag = append(crit.Flag, imap.FlagFlagged)
		has = true
	}
	if q.Since != "" {
		t, err := time.Parse(dateLayout, q.Since)
		if err != nil {
			return nil, false, fmt.Errorf("invalid --since date %q (want YYYY-MM-DD)", q.Since)
		}
		crit.Since = t
		has = true
	}
	if q.Before != "" {
		t, err := time.Parse(dateLayout, q.Before)
		if err != nil {
			return nil, false, fmt.Errorf("invalid --before date %q (want YYYY-MM-DD)", q.Before)
		}
		crit.Before = t
		has = true
	}
	if q.From != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: q.From})
		has = true
	}
	if q.To != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: q.To})
		has = true
	}
	if q.Subject != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: q.Subject})
		has = true
	}
	if q.Text != "" {
		crit.Text = append(crit.Text, q.Text)
		has = true
	}
	return crit, has, nil
}

func summaryFromBuf(b *imapclient.FetchMessageBuffer, folder string, uidValidity uint32) MessageSummary {
	s := MessageSummary{
		UID:         uint32(b.UID),
		UIDValidity: uidValidity,
		Folder:      folder,
		Size:        b.RFC822Size,
		Flags:       flagStrings(b.Flags),
		Seen:        hasFlag(b.Flags, imap.FlagSeen),
	}
	if b.Envelope != nil {
		s.Subject = b.Envelope.Subject
		s.From = formatAddrs(b.Envelope.From)
		s.To = formatAddrs(b.Envelope.To)
		if !b.Envelope.Date.IsZero() {
			s.Date = b.Envelope.Date.Format(displayTime)
		}
	}
	if s.Date == "" && !b.InternalDate.IsZero() {
		s.Date = b.InternalDate.Format(displayTime)
	}
	return s
}

func formatAddrs(addrs []imap.Address) []string {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		addr := a.Mailbox + "@" + a.Host
		if a.Name != "" {
			out = append(out, fmt.Sprintf("%s <%s>", a.Name, addr))
		} else {
			out = append(out, addr)
		}
	}
	return out
}

func flagStrings(flags []imap.Flag) []string {
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = string(f)
	}
	return out
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// parseParts decodes an RFC822 message into its text, HTML and attachments.
// When attachmentsOnly is set, text/html extraction is skipped.
func parseParts(raw []byte, attachmentsOnly bool) (text, html string, atts []Attachment, err error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return "", "", nil, fmt.Errorf("parse message: %w", err)
	}
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return "", "", nil, fmt.Errorf("read part: %w", perr)
		}
		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			if attachmentsOnly {
				continue
			}
			ct, _, _ := h.ContentType()
			body, _ := io.ReadAll(part.Body)
			switch {
			case strings.HasPrefix(ct, "text/html"):
				html = string(body)
			case strings.HasPrefix(ct, "text/plain"):
				text = string(body)
			}
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			ct, _, _ := h.ContentType()
			body, _ := io.ReadAll(part.Body)
			atts = append(atts, Attachment{Filename: filename, ContentType: ct, Data: body})
		}
	}
	return text, html, atts, nil
}
