package index

import (
	"bytes"
	"io"
	"strings"

	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 parts
	"github.com/emersion/go-message/mail"
	"github.com/mxcd/mailotron/internal/render"
)

// maxBody caps the indexed text per message; quoted threads and pasted logs
// add little search value beyond this.
const maxBody = 64 << 10

// ParseDoc extracts headers and searchable text from a raw RFC 5322 message.
// It is lenient: a malformed or undecodable part is skipped rather than
// failing, so a broken mail is still indexed by its headers and never
// re-fetched on every sync.
func ParseDoc(raw []byte) Doc {
	var d Doc
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return d
	}
	h := r.Header
	d.Subject, _ = h.Subject()
	d.MessageID, _ = h.MessageID()
	d.Date, _ = h.Date()
	d.From = addrs(h, "From")
	d.To = addrs(h, "To")
	d.Cc = addrs(h, "Cc")

	var text, html, calendar string
	for {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if strings.Contains(err.Error(), "charset") {
				continue
			}
			break
		}
		ih, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue // attachments are not indexed
		}
		ct, _, _ := ih.ContentType()
		body, err := io.ReadAll(io.LimitReader(part.Body, maxBody*4))
		if err != nil {
			continue
		}
		switch {
		case ct == "text/plain" && strings.TrimSpace(text) == "":
			text = string(body)
		case ct == "text/html" && html == "":
			html = string(body)
		case ct == "text/calendar" && calendar == "":
			calendar = calendarText(string(body))
		}
	}
	if strings.TrimSpace(text) == "" && html != "" {
		text = render.HTMLToText(html)
	}
	if strings.TrimSpace(text) == "" {
		text = calendar // meeting invites often carry no text part
	}
	text = strings.Map(dropInvisible, strings.ToValidUTF8(text, ""))
	if len(text) > maxBody {
		text = strings.ToValidUTF8(text[:maxBody], "")
	}
	d.Body = strings.TrimSpace(text)
	return d
}

// calendarText pulls the human-readable fields out of an iCalendar invite:
// SUMMARY, LOCATION and DESCRIPTION, with RFC 5545 line unfolding and
// unescaping.
func calendarText(ics string) string {
	unfolded := strings.NewReplacer("\r\n ", "", "\r\n\t", "", "\n ", "", "\n\t", "").Replace(ics)
	var out []string
	for _, line := range strings.Split(unfolded, "\n") {
		line = strings.TrimRight(line, "\r")
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name, _, _ = strings.Cut(name, ";") // drop parameters like LANGUAGE=de-DE
		switch strings.ToUpper(name) {
		case "SUMMARY", "LOCATION", "DESCRIPTION":
			value = strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(value)
			if strings.TrimSpace(value) != "" {
				out = append(out, value)
			}
		}
	}
	return strings.Join(out, "\n")
}

// dropInvisible removes zero-width and filler characters that newsletters use
// to pad their inbox preview; they carry no meaning and pollute snippets and
// embeddings.
func dropInvisible(r rune) rune {
	switch r {
	case '\u00ad', '\u034f', '\u061c', '\u115f', '\u1160', '\u17b4', '\u17b5', '\u180e',
		'\u200b', '\u200c', '\u200d', '\u200e', '\u200f', '\u2060', '\u3164', '\ufeff', '\uffa0':
		return -1
	}
	return r
}

func addrs(h mail.Header, key string) string {
	list, err := h.AddressList(key)
	if err != nil || len(list) == 0 {
		v, _ := h.Text(key)
		return v
	}
	out := make([]string, len(list))
	for i, a := range list {
		if a.Name != "" {
			out[i] = a.Name + " <" + a.Address + ">"
		} else {
			out[i] = a.Address
		}
	}
	return strings.Join(out, ", ")
}
