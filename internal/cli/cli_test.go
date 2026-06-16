package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errw bytes.Buffer
	app := New("test", &out, &errw)
	full := append([]string{"mailotron"}, args...)
	err := app.Run(context.Background(), full)
	code = HandleError(app, full, err)
	return out.String(), errw.String(), code
}

func TestGuideJSONCatalog(t *testing.T) {
	stdout, _, code := run(t, "-o", "json", "guide")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var payload struct {
		Guide    string `json:"guide"`
		Commands []struct {
			Path string `json:"path"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("guide json invalid: %v", err)
	}
	if !strings.Contains(payload.Guide, "Agent Operating Guide") {
		t.Error("guide markdown missing")
	}
	paths := map[string]bool{}
	for _, c := range payload.Commands {
		paths[c.Path] = true
	}
	for _, want := range []string{"mailotron send", "mailotron message", "mailotron folder"} {
		if !paths[want] {
			t.Errorf("catalog missing command %q", want)
		}
	}
}

func TestConfigInitAndTemplateList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAILOTRON_CONFIG_DIR", dir)

	if _, _, code := run(t, "config", "init"); code != 0 {
		t.Fatalf("config init exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); err != nil {
		t.Fatalf("config.yml not written: %v", err)
	}

	stdout, _, code := run(t, "-o", "json", "template", "list")
	if code != 0 {
		t.Fatalf("template list exit %d", code)
	}
	var res struct {
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Templates) < 2 {
		t.Errorf("expected seeded templates, got %v", res.Templates)
	}
}

func TestRenderStatelessJSON(t *testing.T) {
	t.Setenv("MAILOTRON_CONFIG_DIR", t.TempDir()) // no config file present

	frame := filepath.Join(t.TempDir(), "frame.mjml")
	if err := os.WriteFile(frame, []byte(`<mjml><mj-body><mj-section><mj-column>{{.Body}}</mj-column></mj-section></mj-body></mjml>`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := run(t, "-o", "json", "render", "--template-file", frame, "--body", "# Hi", "--subject", "S")
	if code != 0 {
		t.Fatalf("render exit %d, stderr=%s", code, stderr)
	}
	var res struct{ HTML, Text, MJML string }
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.HTML, "Hi") || res.Text == "" {
		t.Errorf("render output incomplete: %+v", res)
	}
}

func TestUsageErrorExitCode(t *testing.T) {
	// `template show` with no name is a usage error (exit 2) and emits JSON error.
	stdout, stderr, code := run(t, "-o", "json", "template", "show")
	if code != 2 {
		t.Errorf("want exit 2, got %d", code)
	}
	if stdout != "" {
		t.Errorf("usage error should not write stdout, got %q", stdout)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &e); err != nil {
		t.Fatalf("stderr not JSON error: %q", stderr)
	}
	if e.Error == "" {
		t.Error("expected error message")
	}
}

func TestSliceFlagValuesAreNotCommaSplit(t *testing.T) {
	t.Setenv("MAILOTRON_CONFIG_DIR", t.TempDir())
	frame := filepath.Join(t.TempDir(), "frame.mjml")
	if err := os.WriteFile(frame, []byte(`<mjml><mj-body><mj-section><mj-column><mj-text>{{.Place}}</mj-text>{{.Body}}</mj-column></mj-section></mj-body></mjml>`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := run(t, "-o", "json", "render",
		"--template-file", frame, "--body", "hi", "--subject", "S",
		"--var", "Place=Stuttgart, Germany")
	if code != 0 {
		t.Fatalf("render exit %d: %s", code, stderr)
	}
	var res struct{ HTML string }
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.HTML, "Stuttgart, Germany") {
		t.Errorf("comma-containing var value was split; HTML lacks full value")
	}
}

func TestSendRequiresRecipient(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAILOTRON_CONFIG_DIR", dir)
	t.Setenv("SMTP_PASSWORD", "x")
	t.Setenv("IMAP_PASSWORD", "y")
	run(t, "config", "init")

	// No --to: usage error (exit 2), before any network.
	_, _, code := run(t, "send", "--subject", "S", "--body", "hi", "-t", "default")
	if code != 2 {
		t.Errorf("want exit 2 for missing --to, got %d", code)
	}
}
