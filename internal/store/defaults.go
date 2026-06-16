package store

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mxcd/mailotron/internal/config"
)

//go:embed assets/templates/*.mjml assets/signatures/*.mjml assets/config.example.yml
var assets embed.FS

// DefaultConfigYAML returns the starter config.yml contents.
func DefaultConfigYAML() ([]byte, error) {
	return assets.ReadFile("assets/config.example.yml")
}

// Seed writes the embedded default templates and signatures into the store.
// Existing files are kept unless force is true. It returns the relative names
// of the items written.
func (s *Store) Seed(force bool) ([]string, error) {
	var written []string

	tmpls, err := assets.ReadDir("assets/templates")
	if err != nil {
		return nil, err
	}
	for _, e := range tmpls {
		name := strings.TrimSuffix(e.Name(), templateExt)
		if !force && s.HasTemplate(name) {
			continue
		}
		b, err := assets.ReadFile("assets/templates/" + e.Name())
		if err != nil {
			return written, err
		}
		if err := s.WriteTemplate(name, string(b)); err != nil {
			return written, err
		}
		written = append(written, "templates/"+name)
	}

	sigs, err := assets.ReadDir("assets/signatures")
	if err != nil {
		return written, err
	}
	for _, e := range sigs {
		if !strings.HasSuffix(e.Name(), signatureExt) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), signatureExt)
		if !force && s.HasSignature(name) {
			continue
		}
		mjmlBytes, err := assets.ReadFile("assets/signatures/" + e.Name())
		if err != nil {
			return written, err
		}
		if err := s.WriteSignature(name, string(mjmlBytes)); err != nil {
			return written, err
		}
		written = append(written, "signatures/"+name)
	}

	return written, nil
}

// InitConfig writes the starter config to dst (or the resolved default path
// when dst is empty). It refuses to overwrite an existing file unless force is
// set. The file is written 0600 because it may hold credentials.
func InitConfig(dst string, force bool) (string, error) {
	if dst == "" {
		p, err := config.FilePath("")
		if err != nil {
			return "", err
		}
		dst = p
	}
	if _, err := os.Stat(dst); err == nil && !force {
		return dst, fmt.Errorf("config already exists at %s (use --force to overwrite)", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return dst, err
	}
	b, err := DefaultConfigYAML()
	if err != nil {
		return dst, err
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return dst, err
	}
	return dst, nil
}
