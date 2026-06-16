// Package render turns agent-supplied body content plus an MJML frame template
// and optional signature into a responsive HTML email and a plain-text
// alternative.
//
// Pipeline: body (markdown|mjml|text|html) -> MJML fragment, injected into the
// frame template via text/template, compiled to HTML by gomjml, then reduced to
// plain text. Templates and signatures are always authored in MJML.
package render

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/preslavrachev/gomjml/mjml"
)

// Email block alignment. MJML always centers the fixed-width body column;
// AlignLeft neutralizes that so the email sits flush-left like a normal,
// hand-written mail (the column max-width is unchanged, only its position).
const (
	AlignLeft   = "left"
	AlignCenter = "center"
)

// BodyFormat is the markup the agent supplies the message body in.
type BodyFormat string

const (
	BodyMarkdown BodyFormat = "markdown"
	BodyMJML     BodyFormat = "mjml"
	BodyText     BodyFormat = "text"
	BodyHTML     BodyFormat = "html"
)

// ParseBodyFormat validates a body-format string, defaulting to markdown.
func ParseBodyFormat(s string) (BodyFormat, error) {
	switch BodyFormat(s) {
	case BodyMarkdown, BodyMJML, BodyText, BodyHTML:
		return BodyFormat(s), nil
	case "":
		return BodyMarkdown, nil
	default:
		return "", fmt.Errorf("invalid body format %q (want markdown|mjml|text|html)", s)
	}
}

// Reserved names the renderer injects into the frame template. User variables
// must not collide with these.
var Reserved = []string{"Body", "Signature", "Subject", "PreviewText", "Year", "Date"}

// Input is everything needed to render one message.
type Input struct {
	Frame       string         // MJML frame template (text/template syntax)
	Signature   string         // MJML signature fragment injected as {{.Signature}}
	Body        string         // raw body in BodyFormat
	BodyFormat  BodyFormat     // defaults to markdown
	Subject     string         // injected as {{.Subject}}
	PreviewText string         // injected as {{.PreviewText}}
	Align       string         // "left" (flush-left, normal mail) or "center"; default center
	Vars        map[string]any // arbitrary template variables
}

// Output is the rendered result.
type Output struct {
	HTML string `json:"html"`
	Text string `json:"text"`
	MJML string `json:"mjml"` // composed MJML before compilation, useful for debugging
}

// Render executes the full pipeline. The signature fragment and signature text
// are themselves rendered as templates (so a signature may carry {{.Name}} etc.)
// before being injected into the frame.
func Render(in Input) (*Output, error) {
	if in.Frame == "" {
		return nil, fmt.Errorf("no frame template provided")
	}
	bodyFmt := in.BodyFormat
	if bodyFmt == "" {
		bodyFmt = BodyMarkdown
	}
	bodyMJML, err := bodyToMJML(in.Body, bodyFmt)
	if err != nil {
		return nil, fmt.Errorf("render body: %w", err)
	}

	base := baseData(in.Vars, in.Subject, in.PreviewText)

	sigMJML, err := renderFragment("signature", in.Signature, base)
	if err != nil {
		return nil, err
	}

	frameData := cloneData(base)
	frameData["Body"] = bodyMJML
	frameData["Signature"] = sigMJML

	composed, err := renderFragment("frame", in.Frame, frameData)
	if err != nil {
		return nil, err
	}

	html, err := mjml.Render(composed, mjml.WithCache())
	if err != nil {
		return nil, fmt.Errorf("mjml compile: %w", err)
	}
	if in.Align == AlignLeft {
		html = flushLeft(html)
	}

	// Plain text is the single source of truth derived from the rendered HTML,
	// so the signature (and any frame content) appears exactly once.
	text := htmlToText(html)

	return &Output{HTML: html, Text: text, MJML: composed}, nil
}

// flushLeft neutralizes MJML's auto-centering of the fixed-width body column so
// the email sits flush against the left edge like a normal mail. It only touches
// the body wrapper's centering margin — content alignment (e.g. a centered logo)
// is preserved.
func flushLeft(html string) string {
	html = strings.ReplaceAll(html, "margin:0px auto", "margin:0")
	html = strings.ReplaceAll(html, "margin:0 auto", "margin:0")
	return html
}

// baseData builds the variable set shared by the signature and frame templates.
func baseData(vars map[string]any, subject, preview string) map[string]any {
	d := make(map[string]any, len(vars)+4)
	for k, v := range vars {
		d[k] = v
	}
	d["Subject"] = subject
	d["PreviewText"] = preview
	now := time.Now()
	d["Year"] = now.Year()
	d["Date"] = now.Format("02.01.2006")
	return d
}

func cloneData(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func renderFragment(name, tmplStr string, data map[string]any) (string, error) {
	if tmplStr == "" {
		return "", nil
	}
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s template: %w", name, err)
	}
	return buf.String(), nil
}
