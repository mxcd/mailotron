package agentguide

import (
	"strings"
	"testing"
)

func TestMarkdownNonEmpty(t *testing.T) {
	md := Markdown()
	for _, want := range []string{
		"Agent Operating Guide", "Output contract", "Mental model",
		"mailotron send", "mailotron message", "body-format",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("guide missing expected section/content: %q", want)
		}
	}
}

func TestGuideMeta(t *testing.T) {
	m := GuideMeta()
	if len(m.BodyFormats) != 4 {
		t.Errorf("expected 4 body formats, got %v", m.BodyFormats)
	}
	if m.ExitCodes["0"] == "" || m.ExitCodes["2"] == "" {
		t.Error("exit codes incomplete")
	}
	if m.ConfigPath == "" {
		t.Error("config path missing")
	}
}
