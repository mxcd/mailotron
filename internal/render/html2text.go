package render

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// blockTags emit a newline boundary in the plain-text output. Table rows (tr)
// and cells (td/th) are handled separately so that cells within a row are
// space-separated rather than concatenated.
var blockTags = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Li: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Table: true, atom.Section: true, atom.Header: true, atom.Footer: true,
	atom.Ul: true, atom.Ol: true, atom.Blockquote: true,
}

// skipTags and their contents never appear in plain text. MJML output carries
// large <style> blocks and a <head> that must not leak into the text part.
var skipTags = map[atom.Atom]bool{
	atom.Style: true, atom.Script: true, atom.Head: true, atom.Title: true,
}

type linkFrame struct {
	href string
	pos  int
}

// htmlToText reduces rendered email HTML to readable plain text, preserving
// http(s) link targets as "text (url)" and dropping style/script/head noise.
func htmlToText(input string) string {
	tok := html.NewTokenizer(strings.NewReader(input))
	var b strings.Builder
	skip := 0
	var links []linkFrame

	for {
		tt := tok.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tok.TagName()
			a := atom.Lookup(name)
			switch {
			case skipTags[a]:
				if tt == html.StartTagToken {
					skip++
				}
			case a == atom.Br:
				b.WriteByte('\n')
			case a == atom.A:
				href := ""
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = tok.TagAttr()
					if string(k) == "href" {
						href = string(v)
					}
				}
				if tt == html.StartTagToken {
					links = append(links, linkFrame{href: href, pos: b.Len()})
				}
			case a == atom.Td || a == atom.Th:
				b.WriteByte('\t') // separate adjacent cells; collapsed to a space later
			case blockTags[a]:
				b.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := tok.TagName()
			a := atom.Lookup(name)
			switch {
			case skipTags[a]:
				if skip > 0 {
					skip--
				}
			case a == atom.A:
				if n := len(links); n > 0 {
					lf := links[n-1]
					links = links[:n-1]
					linkText := b.String()[lf.pos:]
					if strings.HasPrefix(lf.href, "http") && !strings.Contains(linkText, lf.href) {
						b.WriteString(" (" + lf.href + ")")
					}
				}
			case a == atom.Tr:
				b.WriteByte('\n')
			case blockTags[a]:
				b.WriteByte('\n')
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(tok.Text())
			}
		}
	}
	return normalizeWhitespace(b.String())
}

func normalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}
