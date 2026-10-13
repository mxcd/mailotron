package render

import (
	"strings"
	"testing"
	"time"
)

func TestQuote(t *testing.T) {
	p := Parent{
		From: `"Doe, Jane" <jane@example.com>`,
		Date: time.Date(2026, 10, 12, 17, 41, 0, 0, time.Local),
		Text: "Hi Max,\r\n\r\nplease <check>.\r\n",
		HTML: `<html><head><style>p{}</style></head><body lang="DE"><p>Hi Max,</p><p>please &lt;check&gt;.</p></body></html>`,
	}
	h, txt := Quote("<html><body><p>Done</p></body></html>", "Done\n", p)

	if !strings.HasPrefix(h, "<html><body><p>Done</p>") || !strings.HasSuffix(h, "</blockquote></body></html>") {
		t.Fatalf("quote not placed before </body>: %s", h)
	}
	for _, want := range []string{
		`<div class="moz-cite-prefix">On 12.10.26 17:41, Doe, Jane wrote:<br></div>`,
		`<blockquote type="cite"`, `<p>please &lt;check&gt;.</p>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("html missing %q", want)
		}
	}
	if strings.Contains(h, "<style>") || strings.Count(h, "<body") != 1 {
		t.Errorf("parent head/body tags leaked: %s", h)
	}
	if wantTxt := "Done\n\nOn 12.10.26 17:41, Doe, Jane wrote:\n> Hi Max,\n>\n> please <check>.\n"; txt != wantTxt {
		t.Errorf("text = %q, want %q", txt, wantTxt)
	}

	// text-only parent is escaped into a <pre>, and no </body> means append
	h, _ = Quote("<p>Done</p>", "Done", Parent{From: "a@b.c", Date: p.Date, Text: "x < y"})
	if !strings.Contains(h, `On 12.10.26 17:41, a@b.c wrote:`) || !strings.Contains(h, `<pre class="moz-quote-pre" wrap="">x &lt; y</pre></blockquote>`) {
		t.Errorf("text-only quote wrong: %s", h)
	}
}
