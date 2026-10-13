package index

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/mxcd/mailotron/internal/config"
)

// maxEmbedText caps the text sent per message; subject, sender and the first
// part of the body carry the meaning. Cost grows with length: on an M1 Max
// qwen3-embedding:0.6b embeds ~12 real mails/s at 1500 chars but ~5/s at 3000.
const maxEmbedText = 1500

// qwen3Instruct is the query instruction qwen3-embedding was trained with;
// documents are embedded without it.
const qwen3Instruct = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: "

// Embedder turns text into vectors via Ollama's /api/embed.
type Embedder struct {
	cfg    config.Embedding
	client *http.Client
}

// NewEmbedder applies defaults to cfg.
func NewEmbedder(cfg config.Embedding) *Embedder {
	if cfg.URL == "" {
		cfg.URL = "http://localhost:11434"
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 16
	}
	if cfg.QueryPrefix == "" && strings.HasPrefix(cfg.Model, "qwen3-embedding") {
		cfg.QueryPrefix = qwen3Instruct
	}
	return &Embedder{cfg: cfg, client: &http.Client{Timeout: 5 * time.Minute}}
}

// Key identifies the vector space; vectors of a different key are re-embedded.
func (e *Embedder) Key() string {
	if e.cfg.Dimensions > 0 {
		return fmt.Sprintf("%s@%d", e.cfg.Model, e.cfg.Dimensions)
	}
	return e.cfg.Model
}

// EmbedQuery embeds a search query (with the model's query instruction).
func (e *Embedder) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	v, err := e.embed(ctx, []string{e.cfg.QueryPrefix + q})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

func (e *Embedder) embed(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]any{"model": e.cfg.Model, "input": texts, "truncate": true}
	if e.cfg.Dimensions > 0 {
		body["dimensions"] = e.cfg.Dimensions
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.cfg.URL, "/")+"/api/embed", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed via %s: %w", e.cfg.URL, err)
	}
	defer resp.Body.Close()
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: decode response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || out.Error != "" {
		return nil, fmt.Errorf("embed: HTTP %d: %s", resp.StatusCode, out.Error)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: got %d vectors for %d texts", len(out.Embeddings), len(texts))
	}
	for _, v := range out.Embeddings {
		normalize(v)
	}
	return out.Embeddings, nil
}

// EmbedPending embeds every message that has no vector for e's model yet, in
// batches committed one by one, so an interrupted run resumes and a model
// change re-embeds everything. It returns how many messages were embedded.
func (x *Index) EmbedPending(ctx context.Context, e *Embedder, progress func(done int)) (int, error) {
	key := e.Key()
	// One vector space at a time: drop vectors of a previous model/dimension.
	if _, err := x.db.ExecContext(ctx, `DELETE FROM embeddings WHERE model != ?`, key); err != nil {
		return 0, err
	}
	done := 0
	for {
		rows, err := x.db.QueryContext(ctx, `SELECT m.id, coalesce(m.subject, ''), coalesce(m.from_addr, ''), coalesce(m.body, '')
			FROM messages m LEFT JOIN embeddings v ON v.msg_id = m.id AND v.model = ?
			WHERE v.msg_id IS NULL ORDER BY m.date DESC LIMIT ?`, key, e.cfg.Batch)
		if err != nil {
			return done, err
		}
		var ids []int64
		var texts []string
		for rows.Next() {
			var id int64
			var subject, from, body string
			if err := rows.Scan(&id, &subject, &from, &body); err != nil {
				rows.Close()
				return done, err
			}
			ids = append(ids, id)
			texts = append(texts, docText(subject, from, body))
		}
		rows.Close()
		if len(ids) == 0 {
			return done, nil
		}
		vecs, err := e.embed(ctx, texts)
		if err != nil {
			return done, err
		}
		err = x.tx(func(tx *sql.Tx) error {
			for i, id := range ids {
				if _, err := tx.Exec(`INSERT OR REPLACE INTO embeddings (msg_id, model, vec) VALUES (?, ?, ?)`,
					id, key, encodeVec(vecs[i])); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return done, err
		}
		done += len(ids)
		if progress != nil {
			progress(done)
		}
	}
}

func docText(subject, from, body string) string {
	t := "Subject: " + subject + "\nFrom: " + from + "\n\n" + body
	if len(t) > maxEmbedText {
		t = strings.ToValidUTF8(t[:maxEmbedText], "")
	}
	return t
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	n := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= n
	}
}

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// dot is the cosine similarity of two normalized vectors (0 on dimension mismatch).
func dot(a []float32, b []byte) float32 {
	if len(b) != 4*len(a) {
		return 0
	}
	var s float32
	for i, x := range a {
		s += x * math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return s
}
