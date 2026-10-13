package imapclient

import (
	"fmt"
	"sort"

	"github.com/emersion/go-imap/v2"
)

// SearchAll runs the query in every selectable folder and returns the newest
// q.Limit matches overall, oldest first like ListMessages.
func (c *Client) SearchAll(q SearchQuery) ([]MessageSummary, error) {
	data, err := c.c.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	var out []MessageSummary
	for _, d := range data {
		if hasAttr(d.Attrs, imap.MailboxAttrNoSelect) || hasAttr(d.Attrs, imap.MailboxAttrNonExistent) {
			continue
		}
		msgs, err := c.ListMessages(d.Mailbox, q)
		if err != nil {
			return nil, err
		}
		out = append(out, msgs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].when.Before(out[j].when) })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[len(out)-q.Limit:]
	}
	return out, nil
}

func hasAttr(attrs []imap.MailboxAttr, want imap.MailboxAttr) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}
