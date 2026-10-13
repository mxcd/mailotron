//go:build e2e

package e2e

import (
	"testing"
)

type indexSyncResult struct {
	Stats struct {
		Folders int `json:"folders"`
		Added   int `json:"added"`
		Removed int `json:"removed"`
	} `json:"stats"`
}

type indexSearchResult struct {
	Count int `json:"count"`
	Hits  []struct {
		Folder  string `json:"folder"`
		UID     uint32 `json:"uid"`
		Subject string `json:"subject"`
		Snippet string `json:"snippet"`
	} `json:"hits"`
}

func indexSync(t *testing.T) indexSyncResult {
	t.Helper()
	var r indexSyncResult
	runJSON(t, &r, "-o", "json", "-a", "green", "index", "sync")
	return r
}

// indexSearch runs an exact word search; semantic/hybrid always fill up to
// --limit with the nearest vectors, so counts are only meaningful for fts.
func indexSearch(t *testing.T, words ...string) indexSearchResult {
	t.Helper()
	var r indexSearchResult
	runJSON(t, &r, append([]string{"-o", "json", "-a", "green", "index", "search", "--mode", "fts"}, words...)...)
	return r
}

// The index syncs straight from IMAP, only touches what changed, and follows
// moves and deletions.
func TestIndexIncrementalSync(t *testing.T) {
	indexSync(t) // baseline over whatever earlier tests left in the mailbox
	if again := indexSync(t); again.Stats.Added != 0 || again.Stats.Removed != 0 {
		t.Fatalf("no-op sync changed the index: %+v", again.Stats)
	}

	subject := "E2E-Index-Zebrafisch"
	runCLI(t, "-a", "green", "send", "--to", imapUser, "--subject", subject, "-t", "default",
		"--body", "Der Kostenvoranschlag für die Aquarienpumpe liegt bei.")
	m := waitForSubject(t, "green", "INBOX", subject)

	if r := indexSync(t); r.Stats.Added != 1 || r.Stats.Removed != 0 {
		t.Fatalf("want exactly one new message indexed, got %+v", r.Stats)
	}
	hits := indexSearch(t, "aquarienpumpe")
	if hits.Count != 1 || hits.Hits[0].Subject != subject || hits.Hits[0].Folder != "INBOX" || hits.Hits[0].UID != m.UID {
		t.Fatalf("body search failed: %+v", hits)
	}
	if h := indexSearch(t, "kostenvoranschlag", "fur"); h.Count != 1 {
		t.Errorf("umlaut-folded search failed: %+v", h)
	}

	runCLI(t, "-a", "green", "folder", "create", "E2E Index Moved")
	runCLI(t, "-a", "green", "message", "move", uid(m.UID), "--to-folder", "E2E Index Moved")
	if r := indexSync(t); r.Stats.Added != 1 || r.Stats.Removed != 1 {
		t.Fatalf("move should be one removal plus one addition, got %+v", r.Stats)
	}
	if h := indexSearch(t, "zebrafisch"); h.Count != 1 || h.Hits[0].Folder != "E2E Index Moved" {
		t.Fatalf("moved message not found in new folder: %+v", h)
	}

	moved := waitForSubject(t, "green", "E2E Index Moved", subject)
	runCLI(t, "-a", "green", "message", "delete", uid(moved.UID), "--folder", "E2E Index Moved", "--hard")
	if r := indexSync(t); r.Stats.Removed != 1 {
		t.Fatalf("hard delete not reflected: %+v", r.Stats)
	}
	if h := indexSearch(t, "zebrafisch"); h.Count != 0 {
		t.Errorf("deleted message still in index: %+v", h)
	}
}

// Sync embeds new messages; semantic search finds a mail by meaning (the fake
// maps "Zahlungserinnerung ... Rechnung" and a query about a Rechnung to the
// same topic) even when the query words do not occur in it.
func TestIndexSemanticSearch(t *testing.T) {
	subject := "E2E-Semantic-Mahnung"
	runCLI(t, "-a", "green", "send", "--to", imapUser, "--subject", subject, "-t", "default",
		"--body", "Zahlungserinnerung: die Rechnung 2026-17 ist seit zwei Wochen offen.")
	waitForSubject(t, "green", "INBOX", subject)

	var r struct {
		Embedded int `json:"embedded"`
	}
	runJSON(t, &r, "-o", "json", "-a", "green", "index", "sync")
	if r.Embedded < 1 {
		t.Fatalf("sync should embed the new message, embedded %d", r.Embedded)
	}
	var again struct {
		Embedded int `json:"embedded"`
	}
	runJSON(t, &again, "-o", "json", "-a", "green", "index", "sync")
	if again.Embedded != 0 {
		t.Errorf("second sync re-embedded %d messages", again.Embedded)
	}

	var res struct {
		Mode string `json:"mode"`
		Hits []struct {
			Subject string `json:"subject"`
		} `json:"hits"`
	}
	runJSON(t, &res, "-o", "json", "-a", "green", "index", "search", "--mode", "semantic", "--limit", "1", "welche rechnung ist unbezahlt")
	if res.Mode != "semantic" || len(res.Hits) != 1 || res.Hits[0].Subject != subject {
		t.Fatalf("semantic search missed: %+v", res)
	}

	runJSON(t, &res, "-o", "json", "-a", "green", "index", "search", "zahlungserinnerung")
	if res.Mode != "hybrid" {
		t.Errorf("default mode with embeddings should be hybrid, got %q", res.Mode)
	}
}

// Excluded folders are never indexed, and messages already indexed from a
// folder that becomes excluded are dropped (here: moved into it).
func TestIndexExcludedFolder(t *testing.T) {
	subject := "E2E-Index-Excluded-Walross"
	runCLI(t, "-a", "green", "send", "--to", imapUser, "--subject", subject, "-t", "default", "--body", "walross")
	m := waitForSubject(t, "green", "INBOX", subject)
	indexSync(t)
	if h := indexSearch(t, "walross"); h.Count != 1 {
		t.Fatalf("precondition: message should be indexed from INBOX: %+v", h)
	}
	runCLI(t, "-a", "green", "folder", "create", "E2E Excluded")
	runCLI(t, "-a", "green", "message", "move", uid(m.UID), "--to-folder", "E2E Excluded")
	indexSync(t)
	if h := indexSearch(t, "walross"); h.Count != 0 {
		t.Errorf("message in an excluded folder is still indexed: %+v", h)
	}
}
