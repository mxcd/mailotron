package config

import (
	"strings"
	"testing"
)

const sampleYAML = `
defaults:
  account: work
  template: default
  signature: work
accounts:
  work:
    from: "Max <max@example.com>"
    outbound:
      type: smtp
      host: smtp.example.com
      port: 587
      tls: starttls
      username: max@example.com
      password: ${TEST_SMTP_PW}
    imap:
      host: imap.example.com
      port: 993
      tls: tls
      username: max@example.com
      password: ${TEST_IMAP_PW}
  news:
    from: "News <news@example.com>"
    outbound:
      type: resend
      api_key: ${TEST_RESEND_KEY}
`

func TestParseAndInterpolate(t *testing.T) {
	t.Setenv("TEST_SMTP_PW", "smtp-secret")
	t.Setenv("TEST_IMAP_PW", "imap-secret")
	t.Setenv("TEST_RESEND_KEY", "re_123")

	cfg, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(cfg.Accounts); got != 2 {
		t.Fatalf("want 2 accounts, got %d", got)
	}
	work := cfg.Accounts["work"]
	if work.Outbound.Password != "smtp-secret" {
		t.Errorf("smtp password not interpolated: %q", work.Outbound.Password)
	}
	if work.IMAP.Password != "imap-secret" {
		t.Errorf("imap password not interpolated: %q", work.IMAP.Password)
	}
	if work.Name != "work" {
		t.Errorf("account name not backfilled: %q", work.Name)
	}
	if cfg.Accounts["news"].Outbound.APIKey != "re_123" {
		t.Errorf("resend api key not interpolated")
	}
}

func TestParseMissingEnvRecorded(t *testing.T) {
	// None of the referenced vars are set: parsing still succeeds, but the
	// missing vars are recorded for readiness checks.
	cfg, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parse should not fail on unset env: %v", err)
	}
	if len(cfg.MissingEnv) == 0 {
		t.Fatal("expected MissingEnv to be populated")
	}
	found := false
	for _, v := range cfg.MissingEnv {
		if v == "TEST_SMTP_PW" {
			found = true
		}
	}
	if !found {
		t.Errorf("MissingEnv should include TEST_SMTP_PW, got %v", cfg.MissingEnv)
	}
}

func TestResolveAccount(t *testing.T) {
	t.Setenv("TEST_SMTP_PW", "x")
	t.Setenv("TEST_IMAP_PW", "y")
	t.Setenv("TEST_RESEND_KEY", "z")
	cfg, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}

	def, err := cfg.Resolve("")
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if def.Name != "work" {
		t.Errorf("default account = %q, want work", def.Name)
	}

	if _, err := cfg.Resolve("missing"); err == nil {
		t.Error("expected error resolving unknown account")
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]struct {
		cfg     Config
		wantErr string
	}{
		"no accounts": {Config{}, "no accounts"},
		"missing from": {Config{Accounts: map[string]*Account{
			"a": {Outbound: &Outbound{Type: OutboundResend, APIKey: "k"}},
		}}, "from is required"},
		"smtp needs host": {Config{Accounts: map[string]*Account{
			"a": {From: "a@b.c", Outbound: &Outbound{Type: OutboundSMTP}},
		}}, "requires host and port"},
		"unknown transport": {Config{Accounts: map[string]*Account{
			"a": {From: "a@b.c", Outbound: &Outbound{Type: "pigeon"}},
		}}, "unknown outbound.type"},
		"bad default": {Config{
			Defaults: Defaults{Account: "nope"},
			Accounts: map[string]*Account{"a": {From: "a@b.c", Outbound: &Outbound{Type: OutboundResend, APIKey: "k"}}},
		}, "does not exist"},
		"bad tls": {Config{Accounts: map[string]*Account{
			"a": {From: "a@b.c", Outbound: &Outbound{Type: OutboundSMTP, Host: "h", Port: 25, TLS: "bogus"}},
		}}, "invalid smtp tls"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestTrashOrDefault(t *testing.T) {
	if (&IMAP{}).TrashOrDefault() != "Trash" {
		t.Error("default trash folder should be Trash")
	}
	if (&IMAP{TrashFolder: "Bin"}).TrashOrDefault() != "Bin" {
		t.Error("configured trash folder should win")
	}
}
