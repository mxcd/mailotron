// Package config loads and validates mailotron's multi-account YAML config.
package config

import (
	"fmt"
	"sort"
)

// Outbound transport types.
const (
	OutboundSMTP   = "smtp"
	OutboundResend = "resend"
)

// TLS modes for SMTP/IMAP connections.
const (
	TLSImplicit = "tls"      // connect over TLS from the start (SMTP 465 / IMAP 993)
	TLSStartTLS = "starttls" // upgrade a plaintext connection (SMTP 587 / IMAP 143)
	TLSNone     = "none"     // no transport security (test backends only)
)

// Config is the root of config.yaml.
type Config struct {
	Defaults Defaults            `yaml:"defaults"`
	Accounts map[string]*Account `yaml:"accounts"`

	// MissingEnv lists ${VAR} references in the config that were not set in the
	// environment. Unset secrets are not a parse error (so secret-free commands
	// like render still work); readiness is reported by `config validate` and
	// surfaced when a transport/IMAP connection is actually used.
	MissingEnv []string `yaml:"-"`
}

// Defaults select which account/template/signature is used when none is given.
type Defaults struct {
	Account   string `yaml:"account"`
	Template  string `yaml:"template"`
	Signature string `yaml:"signature"`
}

// Account bundles an identity with an outbound transport and optional inbound
// IMAP access. An account without an IMAP block is send-only.
type Account struct {
	Name     string    `yaml:"-"`
	From     string    `yaml:"from"`
	Outbound *Outbound `yaml:"outbound"`
	IMAP     *IMAP     `yaml:"imap"`
}

// Outbound describes how mail leaves the account.
type Outbound struct {
	Type string `yaml:"type"` // smtp | resend

	// SMTP fields
	Host     string `yaml:"host,omitempty"`
	Port     int    `yaml:"port,omitempty"`
	TLS      string `yaml:"tls,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`

	// Resend fields
	APIKey string `yaml:"api_key,omitempty"`
}

// IMAP describes inbound mailbox access.
type IMAP struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	TLS         string `yaml:"tls,omitempty"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	TrashFolder string `yaml:"trash_folder,omitempty"`
}

// TrashOrDefault returns the configured trash folder or the IMAP-standard
// "Trash" when unset.
func (i *IMAP) TrashOrDefault() string {
	if i.TrashFolder != "" {
		return i.TrashFolder
	}
	return "Trash"
}

// AccountNames returns the configured account names, sorted for stable output.
func (c *Config) AccountNames() []string {
	names := make([]string, 0, len(c.Accounts))
	for name := range c.Accounts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Resolve returns the named account, falling back to defaults.account when name
// is empty. It errors if no account can be determined.
func (c *Config) Resolve(name string) (*Account, error) {
	if name == "" {
		name = c.Defaults.Account
	}
	if name == "" {
		if len(c.Accounts) == 1 {
			for _, acc := range c.Accounts {
				return acc, nil
			}
		}
		return nil, fmt.Errorf("no account specified and no defaults.account set")
	}
	acc, ok := c.Accounts[name]
	if !ok {
		return nil, fmt.Errorf("account %q not found (have: %v)", name, c.AccountNames())
	}
	return acc, nil
}

func validTLS(mode string) bool {
	switch mode {
	case "", TLSImplicit, TLSStartTLS, TLSNone:
		return true
	default:
		return false
	}
}

// Validate checks structural integrity and returns the first problem found.
func (c *Config) Validate() error {
	if len(c.Accounts) == 0 {
		return fmt.Errorf("no accounts configured")
	}
	for _, name := range c.AccountNames() {
		acc := c.Accounts[name]
		acc.Name = name
		if acc.From == "" {
			return fmt.Errorf("account %q: from is required", name)
		}
		if acc.Outbound == nil {
			return fmt.Errorf("account %q: outbound block is required", name)
		}
		if err := acc.Outbound.validate(name); err != nil {
			return err
		}
		if acc.IMAP != nil {
			if err := acc.IMAP.validate(name); err != nil {
				return err
			}
		}
	}
	if d := c.Defaults.Account; d != "" {
		if _, ok := c.Accounts[d]; !ok {
			return fmt.Errorf("defaults.account %q does not exist", d)
		}
	}
	return nil
}

func (o *Outbound) validate(account string) error {
	switch o.Type {
	case OutboundSMTP:
		if o.Host == "" || o.Port == 0 {
			return fmt.Errorf("account %q: smtp outbound requires host and port", account)
		}
		if !validTLS(o.TLS) {
			return fmt.Errorf("account %q: invalid smtp tls mode %q", account, o.TLS)
		}
	case OutboundResend:
		// api_key may be injected from the environment at runtime; an empty key
		// is a readiness issue checked at send time, not a structural error.
	case "":
		return fmt.Errorf("account %q: outbound.type is required (smtp|resend)", account)
	default:
		return fmt.Errorf("account %q: unknown outbound.type %q", account, o.Type)
	}
	return nil
}

func (i *IMAP) validate(account string) error {
	if i.Host == "" || i.Port == 0 {
		return fmt.Errorf("account %q: imap requires host and port", account)
	}
	if i.Username == "" {
		return fmt.Errorf("account %q: imap requires username", account)
	}
	if !validTLS(i.TLS) {
		return fmt.Errorf("account %q: invalid imap tls mode %q", account, i.TLS)
	}
	return nil
}
