package imapclient

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message/mail"
)

// ThreadInfo is what a reply needs from its parent message.
type ThreadInfo struct {
	MessageID  string // without angle brackets
	References []string
	Subject    string
}

// ReplyHeaders returns the In-Reply-To and References values (RFC 5322 §3.6.4)
// for a message answering this one.
func (t *ThreadInfo) ReplyHeaders() (inReplyTo, references string) {
	if t.MessageID == "" {
		return "", ""
	}
	parent := "<" + t.MessageID + ">"
	refs := make([]string, 0, len(t.References)+1)
	for _, r := range t.References {
		refs = append(refs, "<"+r+">")
	}
	return parent, strings.Join(append(refs, parent), " ")
}

// ReplySubject prefixes the parent subject with "Re: " unless it already has
// a reply prefix (Re:/AW:, any case).
func (t *ThreadInfo) ReplySubject() string {
	lower := strings.ToLower(t.Subject)
	if strings.HasPrefix(lower, "re:") || strings.HasPrefix(lower, "aw:") {
		return t.Subject
	}
	return "Re: " + t.Subject
}

// Thread fetches the threading headers of a message with PEEK, so the parent's
// \Seen flag is left untouched.
func (c *Client) Thread(folder string, uid uint32) (*ThreadInfo, error) {
	if _, err := c.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}
	// The whole header block rather than HEADER.FIELDS: servers echo the field
	// list back in their own casing, which FindBodySection does not match.
	bs := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, Peek: true}
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{bs}}
	bufs, err := c.c.Fetch(imap.UIDSetNum(imap.UID(uid)), opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch uid %d: %w", uid, err)
	}
	if len(bufs) == 0 {
		return nil, fmt.Errorf("message uid %d not found in %q", uid, folder)
	}
	return parseThreadHeaders(bufs[0].FindBodySection(bs))
}

func parseThreadHeaders(raw []byte) (*ThreadInfo, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("server returned no headers")
	}
	r, err := mail.CreateReader(bytes.NewReader(append(raw, "\r\n"...)))
	if err != nil {
		return nil, fmt.Errorf("parse headers: %w", err)
	}
	h := r.Header
	id, err := h.MessageID()
	if err != nil {
		return nil, fmt.Errorf("parse Message-ID: %w", err)
	}
	refs, _ := h.MsgIDList("References")
	subject, _ := h.Subject()
	return &ThreadInfo{MessageID: id, References: refs, Subject: subject}, nil
}
