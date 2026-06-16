package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxcd/mailotron/internal/render"
)

func TestTemplateCRUD(t *testing.T) {
	s := NewAt(t.TempDir())
	if got, _ := s.ListTemplates(); len(got) != 0 {
		t.Fatalf("expected empty store, got %v", got)
	}
	if err := s.WriteTemplate("hello", "<mjml></mjml>"); err != nil {
		t.Fatal(err)
	}
	if !s.HasTemplate("hello") {
		t.Error("HasTemplate false after write")
	}
	c, err := s.ReadTemplate("hello")
	if err != nil || c != "<mjml></mjml>" {
		t.Errorf("read = %q, %v", c, err)
	}
	if names, _ := s.ListTemplates(); len(names) != 1 || names[0] != "hello" {
		t.Errorf("list = %v", names)
	}
	if err := s.RemoveTemplate("hello"); err != nil {
		t.Fatal(err)
	}
	if s.HasTemplate("hello") {
		t.Error("template not removed")
	}
	if _, err := s.ReadTemplate("nope"); err == nil {
		t.Error("expected error reading missing template")
	}
}

func TestSignatureCRUD(t *testing.T) {
	s := NewAt(t.TempDir())
	if err := s.WriteSignature("sig", "<mj-text>{{.Name}}</mj-text>"); err != nil {
		t.Fatal(err)
	}
	sig, err := s.ReadSignature("sig")
	if err != nil {
		t.Fatal(err)
	}
	if sig.MJML == "" {
		t.Errorf("signature incomplete: %+v", sig)
	}
	if err := s.RemoveSignature("sig"); err != nil {
		t.Fatal(err)
	}
	if s.HasSignature("sig") {
		t.Error("signature not removed")
	}
}

func TestInvalidName(t *testing.T) {
	s := NewAt(t.TempDir())
	for _, bad := range []string{"../evil", "a/b", "", "a.b", ".hidden"} {
		if err := s.WriteTemplate(bad, "x"); err == nil {
			t.Errorf("expected error for name %q", bad)
		}
	}
}

func TestSeedAndRenderDefaults(t *testing.T) {
	s := NewAt(t.TempDir())
	written, err := s.Seed(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("seed wrote nothing")
	}
	for _, name := range []string{"default", "newsletter"} {
		if !s.HasTemplate(name) {
			t.Errorf("missing seeded template %q", name)
		}
	}
	if !s.HasSignature("default") {
		t.Error("missing seeded signature")
	}

	// Re-seeding without force is a no-op.
	if again, _ := s.Seed(false); len(again) != 0 {
		t.Errorf("re-seed should write nothing, wrote %v", again)
	}

	// Contract: every shipped default must render cleanly through the real
	// pipeline together with the default signature.
	sig, _ := s.ReadSignature("default")
	vars := map[string]any{
		"Name": "Max", "Role": "Head of Tech", "Company": "Wilde IT",
		"CompanyName": "Wilde IT", "Email": "max@example.com",
		"LogoURL": "https://example.com/logo.png", "UnsubscribeURL": "https://example.com/u",
		"Address": "Somewhere 1", "Website": "https://example.com",
	}
	for _, name := range []string{"default", "newsletter"} {
		frame, _ := s.ReadTemplate(name)
		out, err := render.Render(render.Input{
			Frame:       frame,
			Signature:   sig.MJML,
			Body:        "# Hi\n\nThis is a **test** with [a link](https://example.com).",
			BodyFormat:  render.BodyMarkdown,
			Subject:     "Test",
			PreviewText: "preview",
			Vars:        vars,
		})
		if err != nil {
			t.Fatalf("render seeded template %q: %v", name, err)
		}
		if !strings.Contains(out.HTML, "Max") {
			t.Errorf("template %q render missing signature name", name)
		}
		if !strings.Contains(out.Text, "test") {
			t.Errorf("template %q text missing body:\n%s", name, out.Text)
		}
	}
}

func TestInitConfig(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "config.yml")
	got, err := InitConfig(dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != dst {
		t.Errorf("dst = %q, want %q", got, dst)
	}
	if _, err := InitConfig(dst, false); err == nil {
		t.Error("expected error on existing config without force")
	}
	if _, err := InitConfig(dst, true); err != nil {
		t.Errorf("force init failed: %v", err)
	}
}
