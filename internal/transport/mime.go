package transport

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"

	"github.com/mxcd/mailotron/internal/email"
)

// BuildMIME encodes msg as a complete RFC 5322 message (Date and Message-ID
// set) using the same encoder as the SMTP transport, so a stored draft is
// byte-for-byte what `send` would deliver. Unlike Send it accepts a message
// without recipients, since drafts may be addressed later. It returns the raw
// bytes and the generated Message-ID without angle brackets.
func BuildMIME(msg *email.Message) ([]byte, string, error) {
	m, err := buildGoMailMsg(msg)
	if err != nil {
		return nil, "", err
	}
	id, err := newMessageID(msg.From)
	if err != nil {
		return nil, "", err
	}
	m.SetMessageIDWithValue(id)
	m.SetDate()
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		return nil, "", fmt.Errorf("encode message: %w", err)
	}
	return buf.Bytes(), id, nil
}

// newMessageID returns "<random>@<sender domain>" so the ID is tied to the
// sending domain rather than the local hostname.
func newMessageID(from string) (string, error) {
	domain := "localhost"
	if a, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndexByte(a.Address, '@'); i >= 0 && i < len(a.Address)-1 {
			domain = a.Address[i+1:]
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("message-id: %w", err)
	}
	return hex.EncodeToString(b) + "@" + domain, nil
}
