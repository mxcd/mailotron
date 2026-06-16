// Package email defines the transport-agnostic message model shared between
// the render pipeline and the delivery transports.
package email

// Address is an RFC 5322 address with an optional display name.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Attachment is a file attached to an outbound message. When used as an inline
// image (Message.Inline), ContentID is the value referenced from HTML as
// `cid:<ContentID>`.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	ContentID   string `json:"contentId,omitempty"`
	Data        []byte `json:"-"`
}

// Message is a fully rendered email ready to hand to a transport. The render
// pipeline produces HTML and Text; the transport decides how to encode them.
type Message struct {
	From        string            `json:"from"`
	To          []string          `json:"to"`
	Cc          []string          `json:"cc,omitempty"`
	Bcc         []string          `json:"bcc,omitempty"`
	ReplyTo     string            `json:"replyTo,omitempty"`
	Subject     string            `json:"subject"`
	HTML        string            `json:"-"`
	Text        string            `json:"-"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	Inline      []Attachment      `json:"inline,omitempty"` // inline images referenced as cid:<ContentID>
	Headers     map[string]string `json:"headers,omitempty"`
}

// Recipients returns the union of To, Cc and Bcc — the full envelope recipient
// set a transport must deliver to.
func (m *Message) Recipients() []string {
	out := make([]string, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	out = append(out, m.To...)
	out = append(out, m.Cc...)
	out = append(out, m.Bcc...)
	return out
}
