package index

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Index {
	t.Helper()
	x, err := Open(filepath.Join(t.TempDir(), "idx", "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func put(t *testing.T, x *Index, folder string, d Doc) {
	t.Helper()
	if err := x.Put(folder, d); err != nil {
		t.Fatal(err)
	}
}

func uidsOf(hits []Hit) []uint32 {
	var out []uint32
	for _, h := range hits {
		out = append(out, h.UID)
	}
	return out
}

func TestSearchRankingAndFilters(t *testing.T) {
	x := openTemp(t)
	if err := x.ResetFolder("INBOX", 7); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 9, 1, 10, 0, 0, 0, time.Local)
	put(t, x, "INBOX", Doc{UID: 1, Subject: "Quellcode Übergabe", From: "Markus Müller <mk@valumedia.de>", Body: "bitte den Code", Date: day})
	put(t, x, "INBOX", Doc{UID: 2, Subject: "Mittagessen", From: "a@b.de", Body: "die Quellcode Übergabe ist kein Thema", Date: day.AddDate(0, 0, 10)})
	put(t, x, "INBOX", Doc{UID: 3, Subject: "Rechnung", From: "c@d.de", Body: "Rechnungsnummer 4711", Date: day})

	hits, err := x.Search(Query{Text: "quellcode ubergabe"})
	if err != nil {
		t.Fatal(err)
	}
	if got := uidsOf(hits); len(got) != 2 || got[0] != 1 {
		t.Fatalf("subject match should rank first, diacritics folded: %v", got)
	}
	if !strings.Contains(hits[1].Snippet, "**Quellcode**") {
		t.Errorf("snippet not highlighted: %q", hits[1].Snippet)
	}

	if got, _ := x.Search(Query{Text: "muller"}); len(got) != 1 || got[0].UID != 1 {
		t.Errorf("sender search without umlaut failed: %v", uidsOf(got))
	}
	if got, _ := x.Search(Query{Text: "mk@valumedia.de"}); len(got) != 1 {
		t.Errorf("address with @ and . must not be FTS syntax: %v", uidsOf(got))
	}
	if got, _ := x.Search(Query{Text: "rechnungs*"}); len(got) != 1 || got[0].UID != 3 {
		t.Errorf("prefix search failed: %v", uidsOf(got))
	}
	if got, _ := x.Search(Query{Text: "quellcode", Since: day.AddDate(0, 0, 5)}); len(got) != 1 || got[0].UID != 2 {
		t.Errorf("--since filter failed: %v", uidsOf(got))
	}
	if got, _ := x.Search(Query{Text: "subject:rechnung OR subject:mittagessen", Raw: true}); len(got) != 2 {
		t.Errorf("raw FTS5 query failed: %v", uidsOf(got))
	}
}

func TestIncrementalMaintenance(t *testing.T) {
	x := openTemp(t)
	_ = x.ResetFolder("INBOX", 1)
	_ = x.ResetFolder("Old", 1)
	put(t, x, "INBOX", Doc{UID: 1, Subject: "alpha", Flags: []string{`\Seen`}})
	put(t, x, "INBOX", Doc{UID: 2, Subject: "beta"})
	put(t, x, "Old", Doc{UID: 1, Subject: "gamma"})

	v, uids, found, err := x.FolderState("INBOX")
	if err != nil || !found || v != 1 || len(uids) != 2 {
		t.Fatalf("state = %d %v %v %v", v, uids, found, err)
	}

	if err := x.SetFlags("INBOX", map[uint32][]string{2: {`\Flagged`}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.Search(Query{Text: "beta"}); len(got) != 1 || got[0].Flags[0] != `\Flagged` {
		t.Errorf("flags not updated: %+v", got)
	}

	if err := x.Delete("INBOX", []uint32{1}); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.Search(Query{Text: "alpha"}); len(got) != 0 {
		t.Error("deleted message still searchable")
	}

	n, err := x.PruneFolders(map[string]bool{"INBOX": true})
	if err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if got, _ := x.Search(Query{Text: "gamma"}); len(got) != 0 {
		t.Error("pruned folder still searchable")
	}

	if err := x.ResetFolder("INBOX", 2); err != nil {
		t.Fatal(err)
	}
	if v, uids, _, _ := x.FolderState("INBOX"); v != 2 || len(uids) != 0 {
		t.Errorf("reset kept messages or old validity: %d %v", v, uids)
	}

	st, err := x.Status("")
	if err != nil || st.Folders != 1 || st.Messages != 0 {
		t.Errorf("status = %+v, %v", st, err)
	}
}

func TestSafeMatch(t *testing.T) {
	cases := map[string]string{
		"hello world":       `"hello" "world"`,
		`say "hi"`:          `"say" """hi"""`,
		"rech*":             `"rech"*`,
		"a@b.de NOT-a-word": `"a@b.de" "NOT-a-word"`,
		"  *  ":             "",
	}
	for in, want := range cases {
		if got := safeMatch(in); got != want {
			t.Errorf("safeMatch(%q) = %q, want %q", in, got, want)
		}
	}
}
