//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxcd/mailotron/internal/imapclient"
)

func uid(u uint32) string { return fmt.Sprint(u) }

func hasFolder(folders []imapclient.Mailbox, name string) bool {
	for _, f := range folders {
		if f.Name == name {
			return true
		}
	}
	return false
}

func TestSendAndRetrieveRoundTrip(t *testing.T) {
	subject := "E2E-RoundTrip"
	runCLI(t, "-a", "green", "send",
		"--to", imapUser, "--subject", subject,
		"-t", "default", "-s", "default", "--var", "Name=Agent",
		"--body", "# Hello\n\nThis is a **round trip** with [link](https://example.com).")

	m := waitForSubject(t, "green", "INBOX", subject)

	var detail imapclient.MessageDetail
	runJSON(t, &detail, "-o", "json", "-a", "green", "message", "show", uid(m.UID), "--folder", "INBOX")
	if !strings.Contains(detail.HTML, "round trip") {
		t.Errorf("HTML missing rendered body:\n%s", detail.HTML)
	}
	if !strings.Contains(detail.Text, "Hello") {
		t.Errorf("text part missing body:\n%s", detail.Text)
	}
	if !strings.Contains(detail.Text, "https://example.com") {
		t.Errorf("text part missing link url:\n%s", detail.Text)
	}
}

func TestAttachmentRoundTrip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "report.txt")
	payload := "attachment-payload-123"
	if err := os.WriteFile(src, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	subject := "E2E-Attach"
	runCLI(t, "-a", "green", "send",
		"--to", imapUser, "--subject", subject, "-t", "default",
		"--body", "see attached", "--attach", src)

	m := waitForSubject(t, "green", "INBOX", subject)

	var detail imapclient.MessageDetail
	runJSON(t, &detail, "-o", "json", "-a", "green", "message", "show", uid(m.UID), "--folder", "INBOX")
	found := false
	for _, a := range detail.Attachments {
		if a.Filename == "report.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("attachment not listed in message: %+v", detail.Attachments)
	}

	outDir := t.TempDir()
	runCLI(t, "-a", "green", "message", "download", uid(m.UID),
		"--folder", "INBOX", "--out", outDir, "--attachments-only")
	got, err := os.ReadFile(filepath.Join(outDir, "report.txt"))
	if err != nil {
		t.Fatalf("downloaded attachment missing: %v", err)
	}
	if string(got) != payload {
		t.Errorf("attachment bytes mismatch: %q", got)
	}
}

func TestFolderLifecycle(t *testing.T) {
	runCLI(t, "-a", "green", "folder", "create", "E2EFolder")

	var fl struct {
		Folders []imapclient.Mailbox `json:"folders"`
	}
	runJSON(t, &fl, "-o", "json", "-a", "green", "folder", "list")
	if !hasFolder(fl.Folders, "E2EFolder") {
		t.Fatalf("folder not created: %+v", fl.Folders)
	}

	runCLI(t, "-a", "green", "folder", "rename", "E2EFolder", "E2ERenamed")
	runJSON(t, &fl, "-o", "json", "-a", "green", "folder", "list")
	if hasFolder(fl.Folders, "E2EFolder") || !hasFolder(fl.Folders, "E2ERenamed") {
		t.Fatalf("rename failed: %+v", fl.Folders)
	}

	runCLI(t, "-a", "green", "folder", "delete", "E2ERenamed")
	runJSON(t, &fl, "-o", "json", "-a", "green", "folder", "list")
	if hasFolder(fl.Folders, "E2ERenamed") {
		t.Fatalf("delete failed: %+v", fl.Folders)
	}
}

func TestMessageOrganizeFlagsDelete(t *testing.T) {
	subject := "E2E-Organize"
	runCLI(t, "-a", "green", "send",
		"--to", imapUser, "--subject", subject, "-t", "default", "--body", "organize me")
	m := waitForSubject(t, "green", "INBOX", subject)

	// Mark seen, verify.
	runCLI(t, "-a", "green", "message", "flag", uid(m.UID), "--folder", "INBOX", "--seen")
	var detail imapclient.MessageDetail
	runJSON(t, &detail, "-o", "json", "-a", "green", "message", "show", uid(m.UID), "--folder", "INBOX", "--no-body")
	if !detail.Seen {
		t.Errorf("message not marked seen, flags=%v", detail.Flags)
	}

	// Move to a new folder, verify presence there.
	runCLI(t, "-a", "green", "folder", "create", "E2EArchive")
	runCLI(t, "-a", "green", "message", "move", uid(m.UID), "--folder", "INBOX", "--to-folder", "E2EArchive")
	moved := waitForSubject(t, "green", "E2EArchive", subject)

	// Hard delete from the archive, verify gone.
	runCLI(t, "-a", "green", "message", "delete", uid(moved.UID), "--folder", "E2EArchive", "--hard")
	var res listResult
	runJSON(t, &res, "-o", "json", "-a", "green", "message", "list", "--folder", "E2EArchive", "--limit", "100")
	for _, mm := range res.Messages {
		if mm.Subject == subject {
			t.Error("message still present after hard delete")
		}
	}
}

func TestMessageListFilters(t *testing.T) {
	subject := "E2E-Filter-Unseen"
	runCLI(t, "-a", "green", "send",
		"--to", imapUser, "--subject", subject, "-t", "default", "--body", "filter me")
	waitForSubject(t, "green", "INBOX", subject)

	var res listResult
	runJSON(t, &res, "-o", "json", "-a", "green", "message", "list",
		"--folder", "INBOX", "--unseen", "--subject", "E2E-Filter-Unseen", "--limit", "50")
	found := false
	for _, m := range res.Messages {
		if m.Subject == subject {
			found = true
		}
	}
	if !found {
		t.Errorf("server-side filter did not return the unseen message: %+v", res.Messages)
	}
}

func TestMailpitSend(t *testing.T) {
	subject := "E2E-Mailpit"
	runCLI(t, "-a", "pit", "send",
		"--to", "someone@example.com", "--subject", subject, "-t", "default", "--body", "# Hi mailpit")
	if !mailpitFindSubject(t, subject) {
		t.Fatal("message never arrived in mailpit")
	}
}
