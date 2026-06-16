package transport

import (
	"context"
	"sync"

	"github.com/mxcd/mailotron/internal/email"
)

// Mock is an in-memory Sender for unit tests. It records every message it is
// asked to send and can be primed to return an error.
type Mock struct {
	mu   sync.Mutex
	sent []*email.Message
	Err  error
}

// Send records the message (a shallow copy) unless an error is primed.
func (m *Mock) Send(_ context.Context, msg *email.Message) error {
	if m.Err != nil {
		return m.Err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *msg
	m.sent = append(m.sent, &cp)
	return nil
}

// Messages returns the recorded messages.
func (m *Mock) Messages() []*email.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*email.Message, len(m.sent))
	copy(out, m.sent)
	return out
}

// Reset clears recorded messages.
func (m *Mock) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = nil
}
