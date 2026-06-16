package imapclient

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"
)

// buildMIME constructs a multipart message with a text body and one attachment.
func buildMIME(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	var h mail.Header
	h.SetSubject("Report")
	h.SetAddressList("From", []*mail.Address{{Name: "Max", Address: "max@example.com"}})

	mw, err := mail.CreateWriter(&buf, h)
	if err != nil {
		t.Fatal(err)
	}

	tw, err := mw.CreateInline()
	if err != nil {
		t.Fatal(err)
	}
	var th mail.InlineHeader
	th.Set("Content-Type", "text/plain")
	pw, err := tw.CreatePart(th)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = pw.Write([]byte("hello body"))
	pw.Close()
	tw.Close()

	var ah mail.AttachmentHeader
	ah.Set("Content-Type", "text/plain")
	ah.SetFilename("report.txt")
	aw, err := mw.CreateAttachment(ah)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = aw.Write([]byte("attachment-bytes"))
	aw.Close()
	mw.Close()
	return buf.Bytes()
}

func TestParsePartsExtractsBodyAndAttachment(t *testing.T) {
	raw := buildMIME(t)

	text, _, atts, err := parseParts(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "hello body") {
		t.Errorf("text body not extracted: %q", text)
	}
	if len(atts) != 1 {
		t.Fatalf("want 1 attachment, got %d", len(atts))
	}
	if atts[0].Filename != "report.txt" {
		t.Errorf("attachment filename = %q", atts[0].Filename)
	}
	if string(atts[0].Data) != "attachment-bytes" {
		t.Errorf("attachment data = %q", atts[0].Data)
	}
}

func TestParsePartsAttachmentsOnly(t *testing.T) {
	raw := buildMIME(t)
	text, html, atts, err := parseParts(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if text != "" || html != "" {
		t.Error("attachmentsOnly should skip body extraction")
	}
	if len(atts) != 1 {
		t.Fatalf("want 1 attachment, got %d", len(atts))
	}
}

func TestBuildCriteria(t *testing.T) {
	if _, has, _ := buildCriteria(SearchQuery{}); has {
		t.Error("empty query should yield no criteria")
	}

	crit, has, err := buildCriteria(SearchQuery{Unseen: true, From: "boss@corp.com", Subject: "invoice"})
	if err != nil || !has {
		t.Fatalf("expected criteria, has=%v err=%v", has, err)
	}
	if len(crit.NotFlag) != 1 {
		t.Error("unseen should add NotFlag Seen")
	}
	if len(crit.Header) != 2 {
		t.Errorf("expected 2 header criteria, got %d", len(crit.Header))
	}

	if _, _, err := buildCriteria(SearchQuery{Since: "not-a-date"}); err == nil {
		t.Error("invalid since date should error")
	}
}
