package imapclient

import "testing"

func TestParseThreadHeaders(t *testing.T) {
	raw := []byte("Message-ID: <parent@example.org>\r\n" +
		"References: <root@example.org>\r\n <mid@example.org>\r\n" +
		"Subject: =?utf-8?q?Gesellschafterbeschl=C3=BCsse?=\r\n")
	ti, err := parseThreadHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ti.MessageID != "parent@example.org" || len(ti.References) != 2 {
		t.Fatalf("unexpected thread info: %+v", ti)
	}
	irt, refs := ti.ReplyHeaders()
	if irt != "<parent@example.org>" {
		t.Errorf("In-Reply-To = %q", irt)
	}
	if refs != "<root@example.org> <mid@example.org> <parent@example.org>" {
		t.Errorf("References = %q", refs)
	}
	if got := ti.ReplySubject(); got != "Re: Gesellschafterbeschlüsse" {
		t.Errorf("ReplySubject = %q", got)
	}
}

func TestReplySubjectKeepsExistingPrefix(t *testing.T) {
	for _, s := range []string{"Re: x", "RE: x", "AW: x"} {
		if got := (&ThreadInfo{Subject: s}).ReplySubject(); got != s {
			t.Errorf("%q became %q", s, got)
		}
	}
}

func TestReplyHeadersWithoutMessageID(t *testing.T) {
	if irt, refs := (&ThreadInfo{}).ReplyHeaders(); irt != "" || refs != "" {
		t.Errorf("want empty headers, got %q / %q", irt, refs)
	}
}
