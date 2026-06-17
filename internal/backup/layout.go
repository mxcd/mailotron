// Package backup mirrors an IMAP mailbox to (and from) a directory of plain
// files. The on-disk layout is deterministic — every message lands at the same
// path with byte-identical content across runs — so a content-addressed backup
// tool such as restic deduplicates it and only stores genuine changes. mailotron
// never talks to restic itself; the directory is the contract between them.
package backup

import (
	"strings"
)

// FormatVersion is bumped when the on-disk layout changes incompatibly.
const FormatVersion = 1

const (
	manifestFile = "manifest.json"
	indexFile    = "index.json"
	foldersDir   = "folders"
	messageExt   = ".eml"
)

// Manifest is the top-level description of a backup directory.
type Manifest struct {
	FormatVersion int         `json:"formatVersion"`
	Account       string      `json:"account"`
	Server        string      `json:"server,omitempty"`
	Username      string      `json:"username,omitempty"`
	CreatedAt     string      `json:"createdAt"`
	MessageCount  int         `json:"messageCount"`
	Folders       []FolderRef `json:"folders"`
}

// FolderRef is a manifest entry pointing at one folder's index.
type FolderRef struct {
	Name         string `json:"name"`
	Dir          string `json:"dir"`
	UIDValidity  uint32 `json:"uidValidity"`
	MessageCount int    `json:"messageCount"`
}

// FolderIndex describes one backed-up folder and its messages.
type FolderIndex struct {
	Name        string       `json:"name"`
	UIDValidity uint32       `json:"uidValidity"`
	Attributes  []string     `json:"attributes,omitempty"`
	Messages    []IndexEntry `json:"messages"`
}

// IndexEntry is the metadata kept for one backed-up message. The raw RFC822
// bytes live alongside in File.
type IndexEntry struct {
	UID          uint32   `json:"uid"`
	File         string   `json:"file"`
	MessageID    string   `json:"messageId,omitempty"`
	Flags        []string `json:"flags,omitempty"`
	InternalDate string   `json:"internalDate,omitempty"` // RFC3339
	Size         int64    `json:"size"`
}

// encodeFolder maps an IMAP folder name to a single, reversible path segment.
// Every byte outside [A-Za-z0-9._-] is percent-encoded, so hierarchy delimiters
// ("Clients/Acme") never create nested directories or collide.
func encodeFolder(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-' {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// decodeFolder reverses encodeFolder.
func decodeFolder(seg string) string {
	var b strings.Builder
	for i := 0; i < len(seg); i++ {
		if seg[i] == '%' && i+2 < len(seg) {
			hi, ok1 := unhex(seg[i+1])
			lo, ok2 := unhex(seg[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		b.WriteByte(seg[i])
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}
