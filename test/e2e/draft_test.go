//go:build e2e

package e2e

import (
	"bytes"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/imapclient"
)

type draftResult struct {
	Drafted   bool   `json:"drafted"`
	Folder    string `json:"folder"`
	UID       uint32 `json:"uid"`
	MessageID string `json:"messageId"`
	Subject   string `json:"subject"`
	InReplyTo string `json:"inReplyTo"`
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// rawMessage fetches the verbatim RFC822 bytes of one message.
func rawMessage(t *testing.T, folder string, uid uint32) *mail.Message {
	t.Helper()
	c, err := imapclient.Connect(&config.IMAP{Host: gmHost, Port: atoi(t, gmIMAP), TLS: config.TLSNone, Username: imapLogin, Password: imapPass})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var raw []byte
	if err := c.FetchRaw(folder, []uint32{uid}, func(_ uint32, data []byte) error {
		raw = data
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("draft is not a parseable message: %v", err)
	}
	return m
}

// A draft goes through password_cmd, lands in a folder whose name contains a
// space, carries \Draft, the signature, and is never delivered.
func TestDraftStoredNotSent(t *testing.T) {
	runCLI(t, "-a", "greencmd", "folder", "create", "Entwuerfe Agent")

	subject := "E2E-Draft"
	var res draftResult
	runJSON(t, &res, "-o", "json", "-a", "greencmd", "draft",
		"--to", "Matthias Würtenberger <mw@example.org>", "--cc", "ops@example.org",
		"--subject", subject, "-t", "default", "-s", "default", "--var", "Name=Agent",
		"--preview", "PREVIEW-MARKER", "--body", "Hallo **Welt**")
	if !res.Drafted || res.Folder != "Entwuerfe Agent" || res.MessageID == "" {
		t.Fatalf("unexpected draft result: %+v", res)
	}

	m := waitForSubject(t, "greencmd", "Entwuerfe Agent", subject)
	if !slices.Contains(m.Flags, `\Draft`) || !m.Seen {
		t.Errorf("draft flags = %v, want \\Draft and \\Seen", m.Flags)
	}
	if len(m.Cc) != 1 || !strings.Contains(m.Cc[0], "ops@example.org") {
		t.Errorf("cc not listed: %v", m.Cc)
	}
	if res.UID != 0 && res.UID != m.UID {
		t.Errorf("reported uid %d, listed uid %d", res.UID, m.UID)
	}

	raw := rawMessage(t, "Entwuerfe Agent", m.UID)
	to, err := raw.Header.AddressList("To")
	if err != nil || len(to) != 1 || to[0].Name != "Matthias Würtenberger" || to[0].Address != "mw@example.org" {
		t.Errorf("To header did not survive: %v %v", to, err)
	}

	var detail imapclient.MessageDetail
	runJSON(t, &detail, "-o", "json", "-a", "greencmd", "message", "show", uid(m.UID), "--folder", "Entwuerfe Agent")
	if !strings.Contains(detail.Text, "Welt") {
		t.Errorf("draft body missing:\n%s", detail.Text)
	}
	if strings.Contains(detail.Text, "PREVIEW-MARKER") {
		t.Errorf("preview leaked into the text part:\n%s", detail.Text)
	}

	var inbox listResult
	runJSON(t, &inbox, "-o", "json", "-a", "green", "message", "list", "--subject", subject)
	if inbox.Count != 0 {
		t.Errorf("draft must not be delivered, found %d in INBOX", inbox.Count)
	}
}

// --reply-uid threads the draft under its parent and derives the subject,
// without marking the parent as read.
func TestDraftReplyThreading(t *testing.T) {
	parentSubject := "E2E-Parent"
	runCLI(t, "-a", "green", "send", "--to", imapUser, "--subject", parentSubject, "-t", "default", "--body", "parent")
	parent := waitForSubject(t, "green", "INBOX", parentSubject)
	runCLI(t, "-a", "green", "folder", "create", "Drafts-E2E-Reply")

	var res draftResult
	runJSON(t, &res, "-o", "json", "-a", "green", "draft", "--folder", "Drafts-E2E-Reply",
		"--reply-uid", uid(parent.UID), "--to", imapUser, "-t", "default", "--body", "answer")
	if res.Subject != "Re: "+parentSubject {
		t.Errorf("subject = %q", res.Subject)
	}
	if res.InReplyTo != "<"+parent.MessageID+">" {
		t.Errorf("In-Reply-To = %q, want <%s>", res.InReplyTo, parent.MessageID)
	}

	d := waitForSubject(t, "green", "Drafts-E2E-Reply", "Re: "+parentSubject)
	raw := rawMessage(t, "Drafts-E2E-Reply", d.UID)
	if got := raw.Header.Get("References"); !strings.Contains(got, "<"+parent.MessageID+">") {
		t.Errorf("References = %q", got)
	}

	var after imapclient.MessageDetail
	runJSON(t, &after, "-o", "json", "-a", "green", "message", "show", uid(parent.UID), "--no-body")
	if after.Seen {
		t.Error("drafting a reply must not mark the parent as seen")
	}
}

func TestSearchAllFolders(t *testing.T) {
	subject := "E2E-AllFolders-Needle"
	runCLI(t, "-a", "green", "send", "--to", imapUser, "--subject", subject, "-t", "default", "--body", "needle")
	m := waitForSubject(t, "green", "INBOX", subject)
	runCLI(t, "-a", "green", "folder", "create", "E2E Search Target")
	runCLI(t, "-a", "green", "message", "move", uid(m.UID), "--to-folder", "E2E Search Target")

	var res listResult
	runJSON(t, &res, "-o", "json", "-a", "green", "message", "search", "--all-folders", "--subject", "AllFolders-Needle")
	if res.Count != 1 || res.Messages[0].Folder != "E2E Search Target" {
		t.Fatalf("want one hit in 'E2E Search Target', got %+v", res.Messages)
	}
}
