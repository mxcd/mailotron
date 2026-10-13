package render

import (
	"html"
	"regexp"
	"strings"
	"time"
)

// Parent is the message a reply quotes.
type Parent struct {
	From string // "Name <addr>" or "addr"
	Date time.Time
	Text string
	HTML string
}

var (
	bodyOpenRe  = regexp.MustCompile(`(?is)<body[^>]*>`)
	bodyCloseRe = regexp.MustCompile(`(?is)</body\s*>`)
)

// Attribution is the Thunderbird reply header line,
// "On 12.10.26 17:41, Jane Doe wrote:".
func (p Parent) Attribution() string {
	author := p.From
	if i := strings.Index(author, " <"); i > 0 {
		author = strings.Trim(author[:i], `"`)
	}
	return "On " + p.Date.Local().Format("02.01.06 15:04") + ", " + author + " wrote:"
}

// Quote appends the parent below a rendered reply the way Thunderbird does:
// a moz-cite-prefix line plus a <blockquote type="cite"> in HTML, "> "-prefixed
// lines in text. Inline (cid:) images of the parent are not carried over.
// ponytail: parent <head> styles are dropped, inline styles survive; carry
// them over if quoted Outlook mails look off.
func Quote(replyHTML, replyText string, p Parent) (string, string) {
	attr := html.EscapeString(p.Attribution())
	inner := bodyInner(p.HTML)
	if inner == "" {
		inner = `<pre class="moz-quote-pre" wrap="">` + html.EscapeString(p.Text) + `</pre>`
	}
	block := `<div class="moz-cite-prefix">` + attr + `<br></div>` +
		`<blockquote type="cite" style="margin:0 0 0 .8ex;border-left:2px solid #ccc;padding-left:1ex">` + inner + `</blockquote>`
	if loc := bodyCloseRe.FindAllStringIndex(replyHTML, -1); len(loc) > 0 {
		at := loc[len(loc)-1][0]
		replyHTML = replyHTML[:at] + block + replyHTML[at:]
	} else {
		replyHTML += block
	}

	text := p.Text
	if strings.TrimSpace(text) == "" {
		text = HTMLToText(p.HTML)
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	var b strings.Builder
	b.WriteString(strings.TrimRight(replyText, "\n"))
	b.WriteString("\n\n" + p.Attribution() + "\n")
	for _, l := range lines {
		if l == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> " + l + "\n")
		}
	}
	return replyHTML, b.String()
}

// bodyInner returns the content of an HTML document's <body>, or the whole
// fragment when it has no body tag.
func bodyInner(doc string) string {
	if strings.TrimSpace(doc) == "" {
		return ""
	}
	if open := bodyOpenRe.FindStringIndex(doc); open != nil {
		doc = doc[open[1]:]
	}
	if cl := bodyCloseRe.FindStringIndex(doc); cl != nil {
		doc = doc[:cl[0]]
	}
	return strings.TrimSpace(doc)
}
