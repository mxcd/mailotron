package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	stdmail "net/mail"
	"strings"
	"testing"

	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/email"
)

func sampleMsg() *email.Message {
	return &email.Message{
		From:    "Max <max@example.com>",
		To:      []string{"jane@example.com"},
		Cc:      []string{"ops@example.com"},
		Subject: "Hi",
		HTML:    "<p>hello</p>",
		Text:    "hello",
		Attachments: []email.Attachment{
			{Filename: "a.txt", Data: []byte("file-bytes")},
		},
	}
}

func TestMockSender(t *testing.T) {
	m := &Mock{}
	if err := m.Send(context.Background(), sampleMsg()); err != nil {
		t.Fatal(err)
	}
	if len(m.Messages()) != 1 {
		t.Fatalf("want 1 message, got %d", len(m.Messages()))
	}
	m.Err = errors.New("boom")
	if err := m.Send(context.Background(), sampleMsg()); err == nil {
		t.Error("expected primed error")
	}
	m.Reset()
	if len(m.Messages()) != 0 {
		t.Error("reset did not clear")
	}
}

func TestResendSend(t *testing.T) {
	var gotAuth string
	var gotPayload resendPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer srv.Close()

	r := NewResendWithEndpoint("re_test", srv.URL)
	if err := r.Send(context.Background(), sampleMsg()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotAuth != "Bearer re_test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotPayload.Subject != "Hi" || len(gotPayload.To) != 1 {
		t.Errorf("payload wrong: %+v", gotPayload)
	}
	if len(gotPayload.Attachments) != 1 || gotPayload.Attachments[0].Content == "" {
		t.Errorf("attachment not base64-encoded: %+v", gotPayload.Attachments)
	}
}

func TestResendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"bad from"}`))
	}))
	defer srv.Close()

	r := NewResendWithEndpoint("re_test", srv.URL)
	err := r.Send(context.Background(), sampleMsg())
	if err == nil {
		t.Fatal("expected error on 422")
	}
}

func TestForAccount(t *testing.T) {
	smtpAcc := &config.Account{Name: "s", Outbound: &config.Outbound{Type: config.OutboundSMTP, Host: "h", Port: 25}}
	if s, err := ForAccount(smtpAcc); err != nil {
		t.Fatal(err)
	} else if _, ok := s.(*SMTP); !ok {
		t.Errorf("want *SMTP, got %T", s)
	}

	resendAcc := &config.Account{Name: "r", Outbound: &config.Outbound{Type: config.OutboundResend, APIKey: "k"}}
	if s, err := ForAccount(resendAcc); err != nil {
		t.Fatal(err)
	} else if _, ok := s.(*Resend); !ok {
		t.Errorf("want *Resend, got %T", s)
	}

	bad := &config.Account{Name: "b", Outbound: &config.Outbound{Type: "carrier-pigeon"}}
	if _, err := ForAccount(bad); err == nil {
		t.Error("expected error for unknown transport")
	}
}

func TestBuildGoMailMsg(t *testing.T) {
	m, err := buildGoMailMsg(sampleMsg())
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("nil message")
	}
	// Drafts may be unaddressed, so the encoder accepts no recipients...
	if _, err := buildGoMailMsg(&email.Message{From: "a@b.com", Subject: "x"}); err != nil {
		t.Errorf("unaddressed message should encode: %v", err)
	}
	// ...but SMTP delivery must refuse it.
	s := NewSMTP(&config.Outbound{Type: config.OutboundSMTP, Host: "127.0.0.1", Port: 1})
	if err := s.Send(context.Background(), &email.Message{From: "a@b.com", Subject: "x"}); err == nil {
		t.Error("expected send error with no recipients")
	}
}

func TestBuildMIME(t *testing.T) {
	msg := sampleMsg()
	msg.To = []string{"Matthias Würtenberger <mw@example.org>"}
	msg.Headers = map[string]string{"In-Reply-To": "<parent@example.org>", "References": "<root@example.org> <parent@example.org>"}
	raw, id, err := BuildMIME(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(id, "@example.com") {
		t.Errorf("message-id not tied to sender domain: %q", id)
	}
	parsed, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("output is not a parseable RFC 5322 message: %v", err)
	}
	h := parsed.Header
	if got := h.Get("Message-Id"); got != "<"+id+">" {
		t.Errorf("Message-ID header = %q, want <%s>", got, id)
	}
	if h.Get("Date") == "" {
		t.Error("Date header missing")
	}
	to, err := h.AddressList("To")
	if err != nil || len(to) != 1 || to[0].Address != "mw@example.org" || to[0].Name != "Matthias Würtenberger" {
		t.Errorf("non-ASCII display name did not round-trip: %v %v", to, err)
	}
	if h.Get("In-Reply-To") != "<parent@example.org>" || !strings.Contains(h.Get("References"), "<root@example.org>") {
		t.Errorf("threading headers missing: %q / %q", h.Get("In-Reply-To"), h.Get("References"))
	}
	if h.Get("Cc") == "" {
		t.Error("Cc header missing")
	}
}

func TestBuildGoMailMsgInline(t *testing.T) {
	msg := sampleMsg()
	msg.Inline = []email.Attachment{{Filename: "logo.png", ContentID: "logo", Data: []byte("img-bytes")}}
	if _, err := buildGoMailMsg(msg); err != nil {
		t.Fatalf("embedding inline image failed: %v", err)
	}
}

func TestResendInlineImage(t *testing.T) {
	var gotPayload resendPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewResendWithEndpoint("re_test", srv.URL)
	msg := sampleMsg()
	msg.Inline = []email.Attachment{{Filename: "logo.png", ContentID: "logo", Data: []byte("\x89PNG")}}
	if err := r.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range gotPayload.Attachments {
		if a.ContentID == "logo" && a.Content != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("inline image not sent with content_id: %+v", gotPayload.Attachments)
	}
}
