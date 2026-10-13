// Package index keeps a local SQLite FTS5 full-text index of a mailbox. It is
// synced incrementally from IMAP (see Sync) and stores only searchable text and
// metadata, never attachments or raw messages.
package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mxcd/mailotron/internal/config"
	_ "modernc.org/sqlite" // pure-Go SQLite with FTS5, keeps CGO_ENABLED=0
)

const schemaVersion = 1

const schema = `
CREATE TABLE folders (
	name         TEXT PRIMARY KEY,
	uid_validity INTEGER NOT NULL
);
CREATE TABLE messages (
	id         INTEGER PRIMARY KEY,
	folder     TEXT NOT NULL,
	uid        INTEGER NOT NULL,
	message_id TEXT,
	subject    TEXT,
	from_addr  TEXT,
	to_addr    TEXT,
	cc_addr    TEXT,
	date       INTEGER,
	flags      TEXT,
	size       INTEGER,
	body       TEXT,
	UNIQUE (folder, uid)
);
CREATE INDEX messages_date ON messages (date);
CREATE VIRTUAL TABLE messages_fts USING fts5(
	subject, from_addr, to_addr, cc_addr, body,
	content = 'messages', content_rowid = 'id',
	tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
	INSERT INTO messages_fts (rowid, subject, from_addr, to_addr, cc_addr, body)
	VALUES (new.id, new.subject, new.from_addr, new.to_addr, new.cc_addr, new.body);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
	INSERT INTO messages_fts (messages_fts, rowid, subject, from_addr, to_addr, cc_addr, body)
	VALUES ('delete', old.id, old.subject, old.from_addr, old.to_addr, old.cc_addr, old.body);
END;
CREATE TRIGGER messages_au AFTER UPDATE OF subject, from_addr, to_addr, cc_addr, body ON messages BEGIN
	INSERT INTO messages_fts (messages_fts, rowid, subject, from_addr, to_addr, cc_addr, body)
	VALUES ('delete', old.id, old.subject, old.from_addr, old.to_addr, old.cc_addr, old.body);
	INSERT INTO messages_fts (rowid, subject, from_addr, to_addr, cc_addr, body)
	VALUES (new.id, new.subject, new.from_addr, new.to_addr, new.cc_addr, new.body);
	DELETE FROM embeddings WHERE msg_id = old.id;
END;
CREATE TABLE embeddings (
	msg_id INTEGER PRIMARY KEY,
	model  TEXT NOT NULL,
	vec    BLOB NOT NULL
);
CREATE TRIGGER messages_ad_embedding AFTER DELETE ON messages BEGIN
	DELETE FROM embeddings WHERE msg_id = old.id;
END;
`

// Index is an open index database for one account.
type Index struct {
	db *sql.DB
}

// Doc is one indexed message.
type Doc struct {
	UID       uint32
	MessageID string
	Subject   string
	From      string
	To        string
	Cc        string
	Date      time.Time
	Flags     []string
	Size      int64
	Body      string
}

// PathFor returns the index location of an account: <config dir>/index/<account>.db.
func PathFor(account string) (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "index", account+".db"), nil
}

// Open opens (and on first use creates) the index at path. The file holds the
// plain text of every indexed mail, so directory and file are owner-only.
func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	db.SetMaxOpenConns(1)
	x := &Index{db: db}
	if err := x.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return x, nil
}

// Close closes the database.
func (x *Index) Close() error { return x.db.Close() }

func (x *Index) migrate() error {
	var v int
	if err := x.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("index version: %w", err)
	}
	switch v {
	case schemaVersion:
		return nil
	case 0:
		if _, err := x.db.Exec(schema + fmt.Sprintf("PRAGMA user_version = %d;", schemaVersion)); err != nil {
			return fmt.Errorf("create index schema: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("index schema version %d is newer than this mailotron (%d); delete the index to rebuild it", v, schemaVersion)
	}
}

// FolderState returns the UIDVALIDITY a folder was indexed with and the UIDs
// it holds. found is false for a folder that was never indexed.
func (x *Index) FolderState(folder string) (uidValidity uint32, uids map[uint32]bool, found bool, err error) {
	err = x.db.QueryRow(`SELECT uid_validity FROM folders WHERE name = ?`, folder).Scan(&uidValidity)
	if err == sql.ErrNoRows {
		return 0, map[uint32]bool{}, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	rows, err := x.db.Query(`SELECT uid FROM messages WHERE folder = ?`, folder)
	if err != nil {
		return 0, nil, false, err
	}
	defer rows.Close()
	uids = map[uint32]bool{}
	for rows.Next() {
		var u uint32
		if err := rows.Scan(&u); err != nil {
			return 0, nil, false, err
		}
		uids[u] = true
	}
	return uidValidity, uids, true, rows.Err()
}

// ResetFolder drops a folder's messages and records its (new) UIDVALIDITY.
func (x *Index) ResetFolder(folder string, uidValidity uint32) error {
	return x.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM messages WHERE folder = ?`, folder); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO folders (name, uid_validity) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET uid_validity = excluded.uid_validity`, folder, uidValidity)
		return err
	})
}

// Put inserts or replaces one message. Each call commits on its own, so an
// interrupted sync resumes where it stopped.
func (x *Index) Put(folder string, d Doc) error {
	_, err := x.db.Exec(`INSERT INTO messages
		(folder, uid, message_id, subject, from_addr, to_addr, cc_addr, date, flags, size, body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (folder, uid) DO UPDATE SET
			message_id = excluded.message_id, subject = excluded.subject, from_addr = excluded.from_addr,
			to_addr = excluded.to_addr, cc_addr = excluded.cc_addr, date = excluded.date,
			flags = excluded.flags, size = excluded.size, body = excluded.body`,
		folder, d.UID, d.MessageID, d.Subject, d.From, d.To, d.Cc, unixOrNull(d.Date),
		strings.Join(d.Flags, " "), d.Size, d.Body)
	if err != nil {
		return fmt.Errorf("index %s/%d: %w", folder, d.UID, err)
	}
	return nil
}

// SetFlags updates the flags of already indexed messages; the FTS rows are
// untouched because flags are not searchable text.
func (x *Index) SetFlags(folder string, flags map[uint32][]string) error {
	return x.tx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`UPDATE messages SET flags = ? WHERE folder = ? AND uid = ? AND flags IS NOT ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for uid, f := range flags {
			joined := strings.Join(f, " ")
			if _, err := stmt.Exec(joined, folder, uid, joined); err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete removes messages that are gone from the server.
func (x *Index) Delete(folder string, uids []uint32) error {
	return x.tx(func(tx *sql.Tx) error {
		for _, u := range uids {
			if _, err := tx.Exec(`DELETE FROM messages WHERE folder = ? AND uid = ?`, folder, u); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneFolders removes indexed folders that no longer exist on the server and
// returns how many were dropped.
func (x *Index) PruneFolders(keep map[string]bool) (int, error) {
	rows, err := x.db.Query(`SELECT name FROM folders`)
	if err != nil {
		return 0, err
	}
	var gone []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[name] {
			gone = append(gone, name)
		}
	}
	rows.Close()
	err = x.tx(func(tx *sql.Tx) error {
		for _, name := range gone {
			if _, err := tx.Exec(`DELETE FROM messages WHERE folder = ?`, name); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM folders WHERE name = ?`, name); err != nil {
				return err
			}
		}
		return nil
	})
	return len(gone), err
}

// Status summarizes the index contents.
type Status struct {
	Folders  int    `json:"folders"`
	Messages int    `json:"messages"`
	Embedded int    `json:"embedded"` // messages with a vector for the given model
	Newest   string `json:"newest,omitempty"`
}

// Status returns counts and the date of the newest indexed message. Embedded
// counts vectors of model (empty model: none).
func (x *Index) Status(model string) (Status, error) {
	var s Status
	var newest sql.NullInt64
	err := x.db.QueryRow(`SELECT (SELECT count(*) FROM folders), count(*), max(date),
			(SELECT count(*) FROM embeddings WHERE model = ?) FROM messages`, model).
		Scan(&s.Folders, &s.Messages, &newest, &s.Embedded)
	if newest.Valid {
		s.Newest = time.Unix(newest.Int64, 0).Local().Format(displayTime)
	}
	return s, err
}

func (x *Index) tx(fn func(*sql.Tx) error) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

const displayTime = "02.01.2006 15:04:05"

func unixOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}
