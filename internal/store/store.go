// Package store manages the on-disk template and signature store under the
// mailotron config directory. Templates are <name>.mjml; signatures are
// <name>.mjml plus an optional <name>.txt plain-text variant.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mxcd/mailotron/internal/config"
)

const (
	templateExt  = ".mjml"
	signatureExt = ".mjml"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Store provides typed access to the template and signature directories.
type Store struct {
	templatesDir  string
	signaturesDir string
}

// New builds a Store rooted at the resolved config directory.
func New() (*Store, error) {
	t, err := config.TemplatesDir()
	if err != nil {
		return nil, err
	}
	s, err := config.SignaturesDir()
	if err != nil {
		return nil, err
	}
	return &Store{templatesDir: t, signaturesDir: s}, nil
}

// NewAt builds a Store rooted at an explicit base directory (used in tests).
func NewAt(baseDir string) *Store {
	return &Store{
		templatesDir:  filepath.Join(baseDir, "templates"),
		signaturesDir: filepath.Join(baseDir, "signatures"),
	}
}

// TemplatesDir / SignaturesDir expose the resolved directories.
func (s *Store) TemplatesDir() string  { return s.templatesDir }
func (s *Store) SignaturesDir() string { return s.signaturesDir }

func validateName(kind, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid %s name %q (allowed: letters, digits, '-', '_')", kind, name)
	}
	return nil
}

// ---- Templates ----------------------------------------------------------

// TemplatePath returns the on-disk path for a template name.
func (s *Store) TemplatePath(name string) (string, error) {
	if err := validateName("template", name); err != nil {
		return "", err
	}
	return filepath.Join(s.templatesDir, name+templateExt), nil
}

// HasTemplate reports whether a template exists.
func (s *Store) HasTemplate(name string) bool {
	p, err := s.TemplatePath(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// ReadTemplate returns the MJML source of a template.
func (s *Store) ReadTemplate(name string) (string, error) {
	p, err := s.TemplatePath(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("template %q not found", name)
		}
		return "", err
	}
	return string(b), nil
}

// WriteTemplate creates or overwrites a template.
func (s *Store) WriteTemplate(name, content string) error {
	p, err := s.TemplatePath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.templatesDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// RemoveTemplate deletes a template.
func (s *Store) RemoveTemplate(name string) error {
	p, err := s.TemplatePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("template %q not found", name)
		}
		return err
	}
	return nil
}

// ListTemplates returns the sorted names of stored templates.
func (s *Store) ListTemplates() ([]string, error) {
	return listNames(s.templatesDir, templateExt)
}

// ---- Signatures ---------------------------------------------------------

// Signature is a stored MJML signature fragment. Its plain-text form is derived
// automatically when an email is rendered.
type Signature struct {
	Name string `json:"name"`
	MJML string `json:"mjml"`
}

func (s *Store) signaturePath(name string) (string, error) {
	if err := validateName("signature", name); err != nil {
		return "", err
	}
	return filepath.Join(s.signaturesDir, name+signatureExt), nil
}

// HasSignature reports whether a signature exists.
func (s *Store) HasSignature(name string) bool {
	p, err := s.signaturePath(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// ReadSignature returns a signature with its optional text variant.
func (s *Store) ReadSignature(name string) (*Signature, error) {
	p, err := s.signaturePath(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("signature %q not found", name)
		}
		return nil, err
	}
	return &Signature{Name: name, MJML: string(b)}, nil
}

// WriteSignature creates or overwrites a signature.
func (s *Store) WriteSignature(name, mjml string) error {
	p, err := s.signaturePath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.signaturesDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(mjml), 0o644)
}

// RemoveSignature deletes a signature.
func (s *Store) RemoveSignature(name string) error {
	p, err := s.signaturePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("signature %q not found", name)
		}
		return err
	}
	return nil
}

// ListSignatures returns the sorted names of stored signatures.
func (s *Store) ListSignatures() ([]string, error) {
	return listNames(s.signaturesDir, signatureExt)
}

func listNames(dir, ext string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ext) {
			names = append(names, strings.TrimSuffix(e.Name(), ext))
		}
	}
	sort.Strings(names)
	return names, nil
}
