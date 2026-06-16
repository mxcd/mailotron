// Package transport delivers rendered messages. The Sender interface decouples
// the rest of mailotron from the concrete delivery mechanism (SMTP, Resend, or
// a test mock).
package transport

import (
	"context"
	"fmt"

	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/email"
)

// Sender delivers a rendered message.
type Sender interface {
	Send(ctx context.Context, msg *email.Message) error
}

// Verifier is an optional capability: check that the transport is reachable /
// configured without sending a message. Used by `account test`.
type Verifier interface {
	Verify(ctx context.Context) error
}

// ForAccount builds the Sender configured for an account's outbound transport.
func ForAccount(acc *config.Account) (Sender, error) {
	if acc.Outbound == nil {
		return nil, fmt.Errorf("account %q has no outbound transport", acc.Name)
	}
	switch acc.Outbound.Type {
	case config.OutboundSMTP:
		return NewSMTP(acc.Outbound), nil
	case config.OutboundResend:
		return NewResend(acc.Outbound.APIKey), nil
	default:
		return nil, fmt.Errorf("account %q: unsupported outbound type %q", acc.Name, acc.Outbound.Type)
	}
}
