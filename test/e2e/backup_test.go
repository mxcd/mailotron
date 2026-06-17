//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxcd/mailotron/internal/backup"
	"github.com/mxcd/mailotron/internal/imapclient"
)

func TestBackupRestoreRoundTrip(t *testing.T) {
	const folder = "E2EBackupSrc"
	subjA, subjB := "E2E-Backup-A", "E2E-Backup-B"
	seedMessage(t, folder, subjA, "backup body A")
	seedMessage(t, folder, subjB, "backup body B")

	// Back up just that folder.
	dir := t.TempDir()
	var bres backup.Result
	runJSON(t, &bres, "-o", "json", "-a", "green", "backup", "--out", dir, "--folder", folder)
	if bres.Messages != 2 || bres.Downloaded != 2 || bres.Folders != 1 {
		t.Fatalf("unexpected backup result: %+v", bres)
	}

	// Layout: manifest + two .eml files on disk.
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if n := backupEmlCount(t, dir, folder); n != 2 {
		t.Fatalf("expected 2 .eml files, got %d", n)
	}

	// Incremental: a second backup downloads nothing new.
	var bres2 backup.Result
	runJSON(t, &bres2, "-o", "json", "-a", "green", "backup", "--out", dir, "--folder", folder)
	if bres2.Downloaded != 0 {
		t.Errorf("expected incremental backup to download 0, got %d", bres2.Downloaded)
	}

	// Restore into a fresh namespace.
	const prefix = "Bak-"
	target := prefix + folder
	var rres backup.RestoreResult
	runJSON(t, &rres, "-o", "json", "-a", "green", "restore", "--in", dir, "--prefix", prefix)
	if rres.Restored != 2 || rres.Skipped != 0 {
		t.Fatalf("unexpected restore result: %+v", rres)
	}
	gotA := waitForSubject(t, "green", target, subjA)
	waitForSubject(t, "green", target, subjB)
	if !strings.Contains(strings.Join(gotA.From, ","), imapUser) {
		t.Errorf("restored message lost its From header: %+v", gotA)
	}

	// Idempotent: re-running restore skips both (Message-ID match), no duplicates.
	var rres2 backup.RestoreResult
	runJSON(t, &rres2, "-o", "json", "-a", "green", "restore", "--in", dir, "--prefix", prefix)
	if rres2.Restored != 0 || rres2.Skipped != 2 {
		t.Fatalf("restore not idempotent: %+v", rres2)
	}
	var list listResult
	runJSON(t, &list, "-o", "json", "-a", "green", "message", "list", "--folder", target, "--limit", "100")
	if list.Count != 2 {
		t.Errorf("expected 2 messages after idempotent restore, got %d", list.Count)
	}
}

// --mirror removes the local copy of a message that was deleted on the server.
func TestBackupMirrorPrunesMessages(t *testing.T) {
	const folder = "E2EMirrorMsg"
	seedMessage(t, folder, "E2E-MirrorMsg-A", "a")
	bUID := seedMessage(t, folder, "E2E-MirrorMsg-B", "b")

	dir := t.TempDir()
	runCLI(t, "-a", "green", "backup", "--out", dir, "--folder", folder)
	if n := backupEmlCount(t, dir, folder); n != 2 {
		t.Fatalf("expected 2 .eml before prune, got %d", n)
	}

	runCLI(t, "-a", "green", "message", "delete", uid(bUID), "--folder", folder, "--hard")

	var res backup.Result
	runJSON(t, &res, "-o", "json", "-a", "green", "backup", "--out", dir, "--folder", folder, "--mirror")
	if res.Messages != 1 || res.Pruned != 1 {
		t.Fatalf("--mirror should prune the deleted message: %+v", res)
	}
	if n := backupEmlCount(t, dir, folder); n != 1 {
		t.Fatalf("expected 1 .eml after prune, got %d", n)
	}
}

// A full-mailbox --mirror removes the backup directory of a folder that no
// longer exists on the server. (Folder pruning only applies to an unscoped run.)
// The folder is made to vanish via rename rather than delete — GreenMail returns
// EOF on DELETE of a just-EXAMINEd folder, a quirk real servers do not share.
func TestBackupMirrorPrunesFolders(t *testing.T) {
	const before, after = "E2EPruneBefore", "E2EPruneAfter"
	seedMessage(t, before, "E2E-Prune-1", "prune")

	dir := t.TempDir()
	runCLI(t, "-a", "green", "backup", "--out", dir)
	assertBackupFolder(t, dir, before, true)

	// Rename on the server: the old name disappears from the folder list.
	runCLI(t, "-a", "green", "folder", "rename", before, after)

	var res backup.Result
	runJSON(t, &res, "-o", "json", "-a", "green", "backup", "--out", dir, "--mirror")
	if res.PrunedFolders != 1 {
		t.Fatalf("--mirror should prune the vanished folder: %+v", res)
	}
	assertBackupFolder(t, dir, before, false)
	assertBackupFolder(t, dir, after, true)
}

// Without --mirror, a message deleted on the server is kept in the backup and
// can still be restored.
func TestBackupAdditiveKeepsDeletions(t *testing.T) {
	const folder = "E2EAddSrc"
	subjA, subjB := "E2E-Add-A", "E2E-Add-B"
	seedMessage(t, folder, subjA, "add a")
	bUID := seedMessage(t, folder, subjB, "add b")

	dir := t.TempDir()
	runCLI(t, "-a", "green", "backup", "--out", dir, "--folder", folder)

	runCLI(t, "-a", "green", "message", "delete", uid(bUID), "--folder", folder, "--hard")

	var res backup.Result
	runJSON(t, &res, "-o", "json", "-a", "green", "backup", "--out", dir, "--folder", folder)
	if res.Messages != 2 || res.Pruned != 0 {
		t.Fatalf("additive backup must keep the deleted message: %+v", res)
	}
	if n := backupEmlCount(t, dir, folder); n != 2 {
		t.Fatalf("expected 2 .eml kept, got %d", n)
	}

	// Dry-run restore reports both and creates nothing.
	const prefix = "AddBak-"
	target := prefix + folder
	var dry backup.RestoreResult
	runJSON(t, &dry, "-o", "json", "-a", "green", "restore", "--in", dir, "--prefix", prefix, "--dry-run")
	if !dry.DryRun || dry.Restored != 2 {
		t.Fatalf("dry-run restore: %+v", dry)
	}
	if folderListed(t, "green", target) {
		t.Fatalf("dry-run must not create target folder %q", target)
	}

	// Real restore brings back both, including the server-deleted one.
	var rr backup.RestoreResult
	runJSON(t, &rr, "-o", "json", "-a", "green", "restore", "--in", dir, "--prefix", prefix)
	if rr.Restored != 2 {
		t.Fatalf("restore: %+v", rr)
	}
	waitForSubject(t, "green", target, subjA)
	waitForSubject(t, "green", target, subjB)
}

// Flags and the original date survive a backup→restore round trip via APPEND.
func TestBackupRestorePreservesFlags(t *testing.T) {
	const folder = "E2EFlagSrc"
	subj := "E2E-Flag-1"
	fUID := seedMessage(t, folder, subj, "flag me")
	runCLI(t, "-a", "green", "message", "flag", uid(fUID), "--folder", folder, "--seen")
	runCLI(t, "-a", "green", "message", "flag", uid(fUID), "--folder", folder, "--flagged")

	dir := t.TempDir()
	runCLI(t, "-a", "green", "backup", "--out", dir, "--folder", folder)

	const prefix = "FlagBak-"
	target := prefix + folder
	runCLI(t, "-a", "green", "restore", "--in", dir, "--prefix", prefix)
	r := waitForSubject(t, "green", target, subj)

	var detail imapclient.MessageDetail
	runJSON(t, &detail, "-o", "json", "-a", "green", "message", "show", uid(r.UID), "--folder", target, "--no-body")
	if !detail.Seen {
		t.Errorf("restored message lost \\Seen, flags=%v", detail.Flags)
	}
	if !hasFlagStr(detail.Flags, `\Flagged`) {
		t.Errorf("restored message lost \\Flagged, flags=%v", detail.Flags)
	}
}

// A folder name with characters unsafe for a path is percent-encoded into a
// single directory segment, and still round-trips through restore.
func TestBackupEncodesFolderNames(t *testing.T) {
	const folder = "E2E Enc" // the space must be encoded on disk
	subj := "E2E-Enc-1"
	seedMessage(t, folder, subj, "encode me")

	dir := t.TempDir()
	runCLI(t, "-a", "green", "backup", "--out", dir, "--folder", folder)
	assertBackupFolder(t, dir, "E2E%20Enc", true)

	const prefix = "EncBak-"
	runCLI(t, "-a", "green", "restore", "--in", dir, "--prefix", prefix)
	waitForSubject(t, "green", prefix+folder, subj)
}

// ---- helpers ------------------------------------------------------------

// seedMessage ensures folder exists, sends a message to INBOX, copies it into
// folder, and returns the message's UID within folder.
func seedMessage(t *testing.T, folder, subject, body string) uint32 {
	t.Helper()
	ensureFolder(t, "green", folder)
	runCLI(t, "-a", "green", "send", "--to", imapUser, "--subject", subject, "-t", "default", "--body", body)
	m := waitForSubject(t, "green", "INBOX", subject)
	runCLI(t, "-a", "green", "message", "copy", uid(m.UID), "--folder", "INBOX", "--to-folder", folder)
	return waitForSubject(t, "green", folder, subject).UID
}

func ensureFolder(t *testing.T, account, name string) {
	t.Helper()
	if folderListed(t, account, name) {
		return
	}
	runCLI(t, "-a", account, "folder", "create", name)
}

func folderListed(t *testing.T, account, name string) bool {
	t.Helper()
	var fl struct {
		Folders []imapclient.Mailbox `json:"folders"`
	}
	runJSON(t, &fl, "-o", "json", "-a", account, "folder", "list")
	return hasFolder(fl.Folders, name)
}

// backupEmlCount counts the .eml files under a backup's folder directory. For
// folder names made only of safe characters, encodedFolder equals the name.
func backupEmlCount(t *testing.T, dir, encodedFolder string) int {
	t.Helper()
	g, _ := filepath.Glob(filepath.Join(dir, "folders", encodedFolder, "*.eml"))
	return len(g)
}

func assertBackupFolder(t *testing.T, dir, encodedFolder string, want bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, "folders", encodedFolder))
	if got := err == nil; got != want {
		t.Fatalf("backup folder %q exists=%v, want %v (err=%v)", encodedFolder, got, want, err)
	}
}

func hasFlagStr(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
