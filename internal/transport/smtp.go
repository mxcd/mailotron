package transport

import (
	"bytes"
	"context"
	"fmt"

	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/email"
	"github.com/wneessen/go-mail"
)

// SMTP delivers via an SMTP server using github.com/wneessen/go-mail.
type SMTP struct {
	opts *config.Outbound
}

// NewSMTP builds an SMTP sender from outbound config.
func NewSMTP(opts *config.Outbound) *SMTP {
	return &SMTP{opts: opts}
}

func (s *SMTP) client() (*mail.Client, error) {
	clientOpts := []mail.Option{mail.WithPort(s.opts.Port)}

	switch s.opts.TLS {
	case config.TLSImplicit:
		clientOpts = append(clientOpts, mail.WithSSL())
	case config.TLSStartTLS:
		clientOpts = append(clientOpts, mail.WithTLSPolicy(mail.TLSMandatory))
	case config.TLSNone:
		clientOpts = append(clientOpts, mail.WithTLSPolicy(mail.NoTLS))
	default:
		clientOpts = append(clientOpts, mail.WithTLSPolicy(mail.TLSOpportunistic))
	}

	if s.opts.Username != "" {
		clientOpts = append(clientOpts,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(s.opts.Username),
			mail.WithPassword(s.opts.Password),
		)
	}

	return mail.NewClient(s.opts.Host, clientOpts...)
}

// Verify dials the SMTP server (negotiating TLS/auth) without sending.
func (s *SMTP) Verify(ctx context.Context) error {
	c, err := s.client()
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	if err := c.DialWithContext(ctx); err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	return c.Close()
}

// Send builds a multipart/alternative message and delivers it.
func (s *SMTP) Send(ctx context.Context, msg *email.Message) error {
	m, err := buildGoMailMsg(msg)
	if err != nil {
		return err
	}
	c, err := s.client()
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	if err := c.DialAndSendWithContext(ctx, m); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

func buildGoMailMsg(msg *email.Message) (*mail.Msg, error) {
	m := mail.NewMsg()
	if err := m.From(msg.From); err != nil {
		return nil, fmt.Errorf("invalid from %q: %w", msg.From, err)
	}
	if len(msg.To) == 0 {
		return nil, fmt.Errorf("message has no recipients")
	}
	if err := m.To(msg.To...); err != nil {
		return nil, fmt.Errorf("invalid to: %w", err)
	}
	if len(msg.Cc) > 0 {
		if err := m.Cc(msg.Cc...); err != nil {
			return nil, fmt.Errorf("invalid cc: %w", err)
		}
	}
	if len(msg.Bcc) > 0 {
		if err := m.Bcc(msg.Bcc...); err != nil {
			return nil, fmt.Errorf("invalid bcc: %w", err)
		}
	}
	if msg.ReplyTo != "" {
		if err := m.ReplyTo(msg.ReplyTo); err != nil {
			return nil, fmt.Errorf("invalid reply-to: %w", err)
		}
	}
	m.Subject(msg.Subject)
	for k, v := range msg.Headers {
		m.SetGenHeader(mail.Header(k), v)
	}

	if msg.Text != "" {
		m.SetBodyString(mail.TypeTextPlain, msg.Text)
		if msg.HTML != "" {
			m.AddAlternativeString(mail.TypeTextHTML, msg.HTML)
		}
	} else {
		m.SetBodyString(mail.TypeTextHTML, msg.HTML)
	}

	for _, a := range msg.Attachments {
		name := a.Filename
		data := a.Data
		if err := m.AttachReader(name, bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("attach %q: %w", name, err)
		}
	}
	for _, a := range msg.Inline {
		cid := a.ContentID
		if cid == "" {
			cid = a.Filename
		}
		data := a.Data
		if err := m.EmbedReader(a.Filename, bytes.NewReader(data), mail.WithFileContentID(cid)); err != nil {
			return nil, fmt.Errorf("embed %q: %w", cid, err)
		}
	}
	return m, nil
}
