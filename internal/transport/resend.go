package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mxcd/mailotron/internal/email"
)

const defaultResendEndpoint = "https://api.resend.com/emails"

// Resend delivers via the Resend transactional email HTTP API.
type Resend struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

// NewResend builds a Resend sender.
func NewResend(apiKey string) *Resend {
	return &Resend{
		apiKey:   apiKey,
		endpoint: defaultResendEndpoint,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// NewResendWithEndpoint overrides the API endpoint (used in tests).
func NewResendWithEndpoint(apiKey, endpoint string) *Resend {
	r := NewResend(apiKey)
	r.endpoint = endpoint
	return r
}

type resendAttachment struct {
	Filename  string `json:"filename"`
	Content   string `json:"content"` // base64
	ContentID string `json:"content_id,omitempty"`
}

type resendPayload struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	Cc          []string           `json:"cc,omitempty"`
	Bcc         []string           `json:"bcc,omitempty"`
	ReplyTo     string             `json:"reply_to,omitempty"`
	Subject     string             `json:"subject"`
	HTML        string             `json:"html,omitempty"`
	Text        string             `json:"text,omitempty"`
	Headers     map[string]string  `json:"headers,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
}

// Verify checks that an API key is configured. Resend has no cheap reachability
// probe that avoids sending, so this is a configuration check only.
func (r *Resend) Verify(_ context.Context) error {
	if r.apiKey == "" {
		return fmt.Errorf("resend: missing api key")
	}
	return nil
}

// Send posts the message to the Resend API.
func (r *Resend) Send(ctx context.Context, msg *email.Message) error {
	if r.apiKey == "" {
		return fmt.Errorf("resend: missing api key")
	}
	if len(msg.To) == 0 {
		return fmt.Errorf("message has no recipients")
	}

	payload := resendPayload{
		From:    msg.From,
		To:      msg.To,
		Cc:      msg.Cc,
		Bcc:     msg.Bcc,
		ReplyTo: msg.ReplyTo,
		Subject: msg.Subject,
		HTML:    msg.HTML,
		Text:    msg.Text,
		Headers: msg.Headers,
	}
	for _, a := range msg.Attachments {
		payload.Attachments = append(payload.Attachments, resendAttachment{
			Filename: a.Filename,
			Content:  base64.StdEncoding.EncodeToString(a.Data),
		})
	}
	for _, a := range msg.Inline {
		cid := a.ContentID
		if cid == "" {
			cid = a.Filename
		}
		payload.Attachments = append(payload.Attachments, resendAttachment{
			Filename:  a.Filename,
			Content:   base64.StdEncoding.EncodeToString(a.Data),
			ContentID: cid,
		})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("resend: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("resend: api error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
