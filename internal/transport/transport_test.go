package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	// Missing recipients should error.
	if _, err := buildGoMailMsg(&email.Message{From: "a@b.com", Subject: "x"}); err == nil {
		t.Error("expected error with no recipients")
	}
}
