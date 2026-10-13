package index

import (
	"strings"
	"testing"
)

func TestParseDocPrefersPlainTextAndSkipsAttachments(t *testing.T) {
	raw := "From: =?utf-8?q?J=C3=BCrgen?= <j@example.org>\r\n" +
		"To: a@example.org, B <b@example.org>\r\n" +
		"Subject: Angebot\r\n" +
		"Date: Tue, 29 Sep 2026 11:43:30 +0200\r\n" +
		"Message-ID: <m1@example.org>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=X\r\n\r\n" +
		"--X\r\nContent-Type: multipart/alternative; boundary=Y\r\n\r\n" +
		"--Y\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPlain body text\r\n" +
		"--Y\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>HTML body</p>\r\n" +
		"--Y--\r\n" +
		"--X\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=secret.txt\r\n\r\nATTACHMENT-CONTENT\r\n" +
		"--X--\r\n"
	d := ParseDoc([]byte(raw))
	if d.Subject != "Angebot" || d.MessageID != "m1@example.org" || d.Date.IsZero() {
		t.Fatalf("headers not parsed: %+v", d)
	}
	if d.From != "Jürgen <j@example.org>" || d.To != "a@example.org, B <b@example.org>" {
		t.Errorf("addresses = %q / %q", d.From, d.To)
	}
	if !strings.Contains(d.Body, "Plain body text") || strings.Contains(d.Body, "HTML body") {
		t.Errorf("plain part should win: %q", d.Body)
	}
	if strings.Contains(d.Body, "ATTACHMENT-CONTENT") {
		t.Error("attachment content must not be indexed")
	}
}

func TestParseDocHTMLOnly(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<style>p{}</style><p>Nur <b>HTML</b></p>"
	if d := ParseDoc([]byte(raw)); d.Body != "Nur HTML" {
		t.Errorf("body = %q", d.Body)
	}
}

func TestParseDocGarbageIsLenient(t *testing.T) {
	d := ParseDoc([]byte("\x00\x01 not a mail"))
	_ = d // must not panic; an unparseable mail is indexed empty so it is not refetched forever
}

// Sync reads only a prefix of each message; a cut inside a trailing
// attachment must still yield the headers and the text part.
func TestParseDocTruncatedInsideAttachment(t *testing.T) {
	raw := "Subject: Befund\r\nContent-Type: multipart/mixed; boundary=X\r\n\r\n" +
		"--X\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nAnbei der Befund.\r\n" +
		"--X\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=b.pdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + strings.Repeat("QUJDREVGR0g=", 1000)
	d := ParseDoc([]byte(raw[:len(raw)-5000]))
	if d.Subject != "Befund" || !strings.Contains(d.Body, "Anbei der Befund.") {
		t.Errorf("truncated message lost its text: %+v", d)
	}
}

func TestParseDocCalendarInvite(t *testing.T) {
	raw := "Subject: eValuator - Bug klärung\r\nContent-Type: text/calendar; charset=utf-8; method=REQUEST\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\n" +
		"SUMMARY;LANGUAGE=de-DE:eValuator - Bug klärung\r\n" +
		"LOCATION:Microsoft Teams\\, Raum 2\r\n" +
		"DESCRIPTION:Wir gehen die Fehler bei der Frame-Extraktion durch.\\nAgenda\r\n  folgt.\r\n" + // folding removes CRLF + one space
		"DTSTART:20260128T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	d := ParseDoc([]byte(raw))
	for _, want := range []string{"eValuator - Bug klärung", "Microsoft Teams, Raum 2", "Frame-Extraktion durch.\nAgenda folgt."} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("invite body missing %q:\n%s", want, d.Body)
		}
	}
	if strings.Contains(d.Body, "DTSTART") {
		t.Errorf("raw iCalendar fields leaked: %q", d.Body)
	}
}

func TestParseDocBlankPlainFallsBackToHTML(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: multipart/alternative; boundary=Y\r\n\r\n" +
		"--Y\r\nContent-Type: text/plain\r\n\r\n \r\n" +
		"--Y\r\nContent-Type: text/html\r\n\r\n<p>Echter Inhalt</p>\r\n--Y--\r\n"
	if d := ParseDoc([]byte(raw)); d.Body != "Echter Inhalt" {
		t.Errorf("body = %q", d.Body)
	}
}

func TestParseDocDropsInvisiblePadding(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nEdit on the board\u034f \u200c\u034f \u200c\ufeffdone"
	if d := ParseDoc([]byte(raw)); d.Body != "Edit on the board  done" {
		t.Errorf("body = %q", d.Body)
	}
}
