package imapclient

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/mxcd/mailotron/internal/config"
)

// Client is a connected, authenticated IMAP session.
type Client struct {
	c   *imapclient.Client
	cfg *config.IMAP
}

// Connect dials, negotiates TLS per config, and logs in.
func Connect(cfg *config.IMAP) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("account has no imap configuration")
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var (
		c   *imapclient.Client
		err error
	)
	switch cfg.TLS {
	case config.TLSImplicit, "":
		c, err = imapclient.DialTLS(addr, nil)
	case config.TLSStartTLS:
		c, err = imapclient.DialStartTLS(addr, nil)
	case config.TLSNone:
		c, err = imapclient.DialInsecure(addr, nil)
	default:
		return nil, fmt.Errorf("invalid imap tls mode %q", cfg.TLS)
	}
	if err != nil {
		return nil, fmt.Errorf("imap dial %s: %w", addr, err)
	}

	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return &Client{c: c, cfg: cfg}, nil
}

// Close logs out and closes the connection.
func (c *Client) Close() error {
	if c.c == nil {
		return nil
	}
	_ = c.c.Logout().Wait()
	return c.c.Close()
}

// ---- Folders ------------------------------------------------------------

// ListFolders returns all mailboxes (folders) on the server.
func (c *Client) ListFolders() ([]Mailbox, error) {
	data, err := c.c.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	out := make([]Mailbox, 0, len(data))
	for _, d := range data {
		out = append(out, Mailbox{
			Name:       d.Mailbox,
			Delimiter:  string(d.Delim),
			Attributes: attrStrings(d.Attrs),
		})
	}
	return out, nil
}

// CreateFolder creates a new folder.
func (c *Client) CreateFolder(name string) error {
	if err := c.c.Create(name, nil).Wait(); err != nil {
		return fmt.Errorf("create folder %q: %w", name, err)
	}
	return nil
}

// RenameFolder renames a folder.
func (c *Client) RenameFolder(oldName, newName string) error {
	if err := c.c.Rename(oldName, newName, nil).Wait(); err != nil {
		return fmt.Errorf("rename folder %q -> %q: %w", oldName, newName, err)
	}
	return nil
}

// DeleteFolder deletes a folder.
func (c *Client) DeleteFolder(name string) error {
	if err := c.c.Delete(name).Wait(); err != nil {
		return fmt.Errorf("delete folder %q: %w", name, err)
	}
	return nil
}

func attrStrings(attrs []imap.MailboxAttr) []string {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]string, len(attrs))
	for i, a := range attrs {
		out[i] = string(a)
	}
	return out
}
