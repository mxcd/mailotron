package config

import (
	"strings"
	"testing"
)

func TestResolvePasswordCmd(t *testing.T) {
	i := &IMAP{PasswordCmd: "printf 's3cret\\n'"}
	got, err := i.ResolvePassword()
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cret" {
		t.Errorf("trailing newline not trimmed: %q", got)
	}

	o := &Outbound{Password: "literal"}
	if got, _ := o.ResolvePassword(); got != "literal" {
		t.Errorf("literal password not returned: %q", got)
	}

	bad := &Outbound{PasswordCmd: "echo nope >&2; exit 3"}
	_, err = bad.ResolvePassword()
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("failing command should surface its stderr, got %v", err)
	}
}

func TestPasswordAndCmdMutuallyExclusive(t *testing.T) {
	yaml := `
accounts:
  a:
    from: a@b.com
    outbound: { type: smtp, host: h, port: 25 }
    imap: { host: h, port: 993, username: u, password: x, password_cmd: "echo y" }
`
	if _, err := Parse([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("want mutual-exclusion error, got %v", err)
	}
}

func TestAccountLevelDefaultsAndDrafts(t *testing.T) {
	yaml := `
accounts:
  wit:
    from: a@b.com
    template: plain
    signature: wit
    outbound: { type: smtp, host: h, port: 465, password_cmd: "echo pw" }
    imap: { host: h, port: 993, username: u, password_cmd: "echo pw", drafts_folder: "INBOX.03 Entwuerfe Betty" }
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	acc := cfg.Accounts["wit"]
	if acc.Template != "plain" || acc.Signature != "wit" {
		t.Errorf("account template/signature not parsed: %+v", acc)
	}
	if got := acc.IMAP.DraftsOrDefault(); got != "INBOX.03 Entwuerfe Betty" {
		t.Errorf("drafts folder = %q", got)
	}
	if got := (&IMAP{}).DraftsOrDefault(); got != "Drafts" {
		t.Errorf("default drafts folder = %q", got)
	}
}
