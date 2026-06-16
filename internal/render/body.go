package render

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

// md converts agent Markdown to HTML. GFM gives tables, strikethrough and
// autolinks; WithUnsafe lets agents drop in raw HTML when they need it.
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
)

// bodyToMJML converts the raw body into a column-safe MJML fragment. For
// markdown/text/html the content is wrapped in <mj-text>; mjml is passed
// through verbatim (the author is responsible for valid MJML).
func bodyToMJML(body string, f BodyFormat) (string, error) {
	switch f {
	case BodyMarkdown:
		h, err := markdownToHTML(body)
		if err != nil {
			return "", err
		}
		return wrapText(h), nil
	case BodyHTML:
		return wrapText(body), nil
	case BodyText:
		escaped := html.EscapeString(body)
		escaped = strings.ReplaceAll(escaped, "\n", "<br/>\n")
		return wrapText(escaped), nil
	case BodyMJML:
		return body, nil
	default:
		return "", fmt.Errorf("invalid body format %q", f)
	}
}

func markdownToHTML(src string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func wrapText(htmlContent string) string {
	return "<mj-text>\n" + htmlContent + "\n</mj-text>"
}
