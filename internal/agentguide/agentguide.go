// Package agentguide embeds the agent-facing operating manual so it ships in
// the binary and is retrievable via `mailotron guide`.
package agentguide

import _ "embed"

//go:embed AGENT_GUIDE.md
var guideMarkdown string

// Markdown returns the full agent operating guide as Markdown.
func Markdown() string {
	return guideMarkdown
}

// Meta is static, machine-readable context that complements the live command
// catalog emitted by `mailotron guide -o json`.
type Meta struct {
	BodyFormats  []string          `json:"bodyFormats"`
	ReservedVars []string          `json:"reservedVars"`
	ExitCodes    map[string]string `json:"exitCodes"`
	EnvVars      map[string]string `json:"envVars"`
	ConfigPath   string            `json:"configPathDefault"`
}

// GuideMeta returns the static metadata block for the JSON guide.
func GuideMeta() Meta {
	return Meta{
		BodyFormats:  []string{"markdown", "mjml", "text", "html"},
		ReservedVars: []string{"Subject", "PreviewText", "Year", "Date", "Body", "Signature"},
		ExitCodes: map[string]string{
			"0": "success",
			"1": "runtime error (send/imap/not-found)",
			"2": "usage error (bad flags or arguments)",
		},
		EnvVars: map[string]string{
			"MAILOTRON_CONFIG":     "override config file path",
			"MAILOTRON_CONFIG_DIR": "override config/store directory",
		},
		ConfigPath: "~/.mailotron/config.yml",
	}
}
