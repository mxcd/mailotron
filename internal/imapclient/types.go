// Package imapclient wraps emersion/go-imap/v2 with a small, agent-friendly API
// for folder and message management.
package imapclient

// Mailbox is an IMAP folder ("directory").
type Mailbox struct {
	Name       string   `json:"name"`
	Delimiter  string   `json:"delimiter,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// MessageSummary is the envelope-level view of a message.
type MessageSummary struct {
	UID         uint32   `json:"uid"`
	UIDValidity uint32   `json:"uidValidity"`
	Folder      string   `json:"folder"`
	Subject     string   `json:"subject"`
	From        []string `json:"from,omitempty"`
	To          []string `json:"to,omitempty"`
	Date        string   `json:"date,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	Size        int64    `json:"size"`
	Seen        bool     `json:"seen"`
}

// AttachmentInfo describes an attachment on a fetched message.
type AttachmentInfo struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	Size        int    `json:"size"`
}

// MessageDetail is a message with its decoded body parts and attachments.
type MessageDetail struct {
	MessageSummary
	Text        string           `json:"text,omitempty"`
	HTML        string           `json:"html,omitempty"`
	Attachments []AttachmentInfo `json:"attachments,omitempty"`
}

// SearchQuery is the structured filter for listing/searching messages.
type SearchQuery struct {
	Unseen  bool
	Flagged bool
	Since   string // YYYY-MM-DD
	Before  string // YYYY-MM-DD
	From    string
	To      string
	Subject string
	Text    string
	Limit   int
}
