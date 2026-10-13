package index

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Search modes.
const (
	ModeFTS      = "fts"      // BM25 full-text
	ModeSemantic = "semantic" // cosine similarity of embeddings
	ModeHybrid   = "hybrid"   // reciprocal rank fusion of both
)

// candidates per side considered for hybrid fusion
const hybridPool = 50

// rrfK dampens rank differences in reciprocal rank fusion (standard value).
const rrfK = 60

// Query is a search against the index.
type Query struct {
	Text   string    // words; every word must match in FTS (trailing * = prefix)
	Raw    bool      // pass Text to FTS5 unchanged (AND/OR/NOT, NEAR, subject:word)
	Mode   string    // fts (default) | semantic | hybrid
	Vector []float32 // query embedding, required for semantic/hybrid
	Model  string    // embedding key the Vector belongs to
	Folder string    // restrict to one folder
	Since  time.Time // on/after
	Before time.Time // before
	Limit  int
}

// Hit is one ranked search result; Folder + UID address the live message.
// Score is higher-is-better within one search (BM25, cosine or fused rank).
type Hit struct {
	Folder    string   `json:"folder"`
	UID       uint32   `json:"uid"`
	MessageID string   `json:"messageId,omitempty"`
	Date      string   `json:"date,omitempty"`
	From      string   `json:"from,omitempty"`
	To        string   `json:"to,omitempty"`
	Subject   string   `json:"subject"`
	Flags     []string `json:"flags,omitempty"`
	Snippet   string   `json:"snippet"`
	Score     float64  `json:"score"`
}

type ranked struct {
	id    int64
	score float64
}

// Search returns the best matches, most relevant first.
func (x *Index) Search(q Query) ([]Hit, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	mode := q.Mode
	if mode == "" {
		mode = ModeFTS
	}
	if mode != ModeFTS && len(q.Vector) == 0 {
		return nil, fmt.Errorf("%s search needs a query embedding", mode)
	}

	snippets := map[int64]string{}
	var list []ranked
	var err error
	switch mode {
	case ModeFTS:
		list, err = x.ftsRanked(q, limit, snippets)
	case ModeSemantic:
		list, err = x.vecRanked(q, limit)
	case ModeHybrid:
		var fts, vec []ranked
		if fts, err = x.ftsRanked(q, hybridPool, snippets); err != nil {
			return nil, err
		}
		if vec, err = x.vecRanked(q, hybridPool); err != nil {
			return nil, err
		}
		list = fuse(limit, fts, vec)
	default:
		return nil, fmt.Errorf("unknown search mode %q (want fts|semantic|hybrid)", mode)
	}
	if err != nil {
		return nil, err
	}
	return x.load(list, snippets)
}

// ftsRanked runs the BM25 query. Subject hits weigh most, then sender, then
// recipients and body. A query that is empty after sanitizing (only
// punctuation) yields no FTS hits rather than an error, so hybrid still works.
func (x *Index) ftsRanked(q Query, limit int, snippets map[int64]string) ([]ranked, error) {
	match := q.Text
	if !q.Raw {
		match = safeMatch(q.Text)
	}
	if strings.TrimSpace(match) == "" {
		if q.Mode == ModeHybrid {
			return nil, nil
		}
		return nil, fmt.Errorf("empty search query")
	}
	where, args := filters(q)
	rows, err := x.db.Query(`SELECT m.id, snippet(messages_fts, 4, '**', '**', ' … ', 16),
			-bm25(messages_fts, 5.0, 2.0, 1.0, 1.0, 1.0) AS score
		FROM messages_fts JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?`+where+` ORDER BY score DESC LIMIT ?`,
		append(append([]any{match}, args...), limit)...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var out []ranked
	for rows.Next() {
		var r ranked
		var snip string
		if err := rows.Scan(&r.id, &snip, &r.score); err != nil {
			return nil, err
		}
		snippets[r.id] = snip
		out = append(out, r)
	}
	return out, rows.Err()
}

// vecRanked scores every stored vector of the query's model against it.
//
// ponytail: brute-force scan of all vectors per query (~0.1 s per 10k mails at
// 1024 dims); move to an ANN index if mailboxes reach hundreds of thousands.
func (x *Index) vecRanked(q Query, limit int) ([]ranked, error) {
	where, args := filters(q)
	rows, err := x.db.Query(`SELECT v.msg_id, v.vec FROM embeddings v JOIN messages m ON m.id = v.msg_id
		WHERE v.model = ?`+where, append([]any{q.Model}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}
	defer rows.Close()
	var out []ranked
	for rows.Next() {
		var id int64
		var vec []byte
		if err := rows.Scan(&id, &vec); err != nil {
			return nil, err
		}
		out = append(out, ranked{id: id, score: float64(dot(q.Vector, vec))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// fuse merges ranked lists with reciprocal rank fusion: score = Σ 1/(k+rank).
func fuse(limit int, lists ...[]ranked) []ranked {
	scores := map[int64]float64{}
	for _, l := range lists {
		for rank, r := range l {
			scores[r.id] += 1 / float64(rrfK+rank+1)
		}
	}
	out := make([]ranked, 0, len(scores))
	for id, s := range scores {
		out = append(out, ranked{id: id, score: s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].id > out[j].id
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func filters(q Query) (string, []any) {
	var where string
	var args []any
	if q.Folder != "" {
		where += ` AND m.folder = ?`
		args = append(args, q.Folder)
	}
	if !q.Since.IsZero() {
		where += ` AND m.date >= ?`
		args = append(args, q.Since.Unix())
	}
	if !q.Before.IsZero() {
		where += ` AND m.date < ?`
		args = append(args, q.Before.Unix())
	}
	return where, args
}

// load fetches the display fields for ranked ids, keeping their order. Hits
// without an FTS snippet (semantic-only) get the start of the body.
func (x *Index) load(list []ranked, snippets map[int64]string) ([]Hit, error) {
	hits := make([]Hit, 0, len(list))
	for _, r := range list {
		var h Hit
		var date sql.NullInt64
		var msgID, from, to, subject, flags, body sql.NullString
		err := x.db.QueryRow(`SELECT folder, uid, message_id, date, from_addr, to_addr, subject, flags, substr(body, 1, 200)
			FROM messages WHERE id = ?`, r.id).
			Scan(&h.Folder, &h.UID, &msgID, &date, &from, &to, &subject, &flags, &body)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		h.MessageID, h.From, h.To, h.Subject = msgID.String, from.String, to.String, subject.String
		if flags.String != "" {
			h.Flags = strings.Fields(flags.String)
		}
		if date.Valid {
			h.Date = time.Unix(date.Int64, 0).Local().Format(displayTime)
		}
		snippet := snippets[r.id]
		if snippet == "" {
			snippet = body.String
		}
		h.Snippet = strings.Join(strings.Fields(snippet), " ") // one line, also for text output
		h.Score = r.score
		hits = append(hits, h)
	}
	return hits, nil
}

// safeMatch turns free text into an FTS5 query that cannot be a syntax error:
// every word becomes a quoted phrase (so "@", "-" or ":" in an address are
// literal), implicitly ANDed; a trailing * keeps prefix matching.
func safeMatch(text string) string {
	var parts []string
	for _, w := range strings.Fields(text) {
		prefix := strings.HasSuffix(w, "*")
		w = strings.TrimRight(w, "*")
		if w == "" {
			continue
		}
		p := `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
		if prefix {
			p += "*"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}
