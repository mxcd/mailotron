package index

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mxcd/mailotron/internal/config"
)

// fakeOllama embeds text into 3 dims by topic keyword, and records inputs.
type fakeOllama struct {
	mu     sync.Mutex
	inputs []string
}

func (f *fakeOllama) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.inputs = append(f.inputs, req.Input...)
		f.mu.Unlock()
		var out [][]float32
		for _, in := range req.Input {
			l := strings.ToLower(in)
			v := []float32{0.01, 0.01, 0.01}
			for i, kw := range []string{"hund", "katze", "rechnung"} {
				if strings.Contains(l, kw) {
					v[i] = 1
				}
			}
			out = append(out, v)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pending(t *testing.T, x *Index, e *Embedder) int {
	t.Helper()
	n, err := x.EmbedPending(context.Background(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEmbedIncrementalAndInvalidation(t *testing.T) {
	f := &fakeOllama{}
	srv := f.server(t)
	x := openTemp(t)
	_ = x.ResetFolder("INBOX", 1)
	put(t, x, "INBOX", Doc{UID: 1, Subject: "Tierarzt", Body: "Der Hund hustet"})
	put(t, x, "INBOX", Doc{UID: 2, Subject: "Offene Posten", Body: "Ihre Rechnung 4711"})
	put(t, x, "INBOX", Doc{UID: 3, Subject: "Futter", Body: "Die Katze mag Fisch"})

	e := NewEmbedder(config.Embedding{URL: srv.URL, Model: "fake", Batch: 2})
	if n := pending(t, x, e); n != 3 {
		t.Fatalf("first run embedded %d, want 3", n)
	}
	if n := pending(t, x, e); n != 0 {
		t.Fatalf("second run embedded %d, want 0 (incremental)", n)
	}

	if err := x.SetFlags("INBOX", map[uint32][]string{1: {`\Seen`}}); err != nil {
		t.Fatal(err)
	}
	if n := pending(t, x, e); n != 0 {
		t.Errorf("a flag change must not invalidate the vector (re-embedded %d)", n)
	}

	put(t, x, "INBOX", Doc{UID: 3, Subject: "Futter", Body: "Der Hund mag Fisch"})
	if n := pending(t, x, e); n != 1 {
		t.Errorf("changed text must be re-embedded once, got %d", n)
	}

	if st, _ := x.Status("fake"); st.Embedded != 3 {
		t.Errorf("status embedded = %d", st.Embedded)
	}
	_ = x.Delete("INBOX", []uint32{2})
	if st, _ := x.Status("fake"); st.Embedded != 2 {
		t.Errorf("deleting a message must drop its vector, embedded = %d", st.Embedded)
	}

	other := NewEmbedder(config.Embedding{URL: srv.URL, Model: "fake", Dimensions: 2})
	if other.Key() == e.Key() {
		t.Fatal("dimensions must be part of the vector-space key")
	}
	if n := pending(t, x, other); n != 2 {
		t.Errorf("a new model key must re-embed everything, got %d", n)
	}
	if st, _ := x.Status("fake"); st.Embedded != 0 {
		t.Errorf("vectors of the previous model must be dropped, %d left", st.Embedded)
	}
}

func TestSemanticAndHybridSearch(t *testing.T) {
	f := &fakeOllama{}
	srv := f.server(t)
	x := openTemp(t)
	_ = x.ResetFolder("INBOX", 1)
	put(t, x, "INBOX", Doc{UID: 1, Subject: "Tierarzt", Body: "Der Hund hustet seit Tagen"})
	put(t, x, "INBOX", Doc{UID: 2, Subject: "Offene Posten", Body: "Ihre Rechnung 4711 ist fällig"})
	put(t, x, "INBOX", Doc{UID: 3, Subject: "Futter", Body: "Die Katze mag Fisch"})

	e := NewEmbedder(config.Embedding{URL: srv.URL, Model: "qwen3-embedding:0.6b"})
	pending(t, x, e)

	qv, err := e.EmbedQuery(context.Background(), "Zahlung für die Rechnung")
	if err != nil {
		t.Fatal(err)
	}
	if last := f.inputs[len(f.inputs)-1]; !strings.HasPrefix(last, "Instruct: ") {
		t.Errorf("qwen3 queries need the instruct prefix, sent %q", last)
	}
	for _, in := range f.inputs[:3] {
		if strings.HasPrefix(in, "Instruct: ") {
			t.Errorf("documents must be embedded without the prefix: %q", in)
		}
	}

	hits, err := x.Search(Query{Text: "Zahlung", Mode: ModeSemantic, Vector: qv, Model: e.Key(), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].UID != 2 {
		t.Fatalf("semantic search should find the invoice without a word match: %+v", hits)
	}
	if hits[0].Snippet == "" {
		t.Error("semantic hits need a body snippet")
	}

	// "Hund" matches UID 1 by word; the vector points at the invoice: fusion keeps both.
	hits, err = x.Search(Query{Text: "Hund", Mode: ModeHybrid, Vector: qv, Model: e.Key(), Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint32]bool{}
	for _, h := range hits {
		got[h.UID] = true
	}
	if !got[1] || !got[2] {
		t.Errorf("hybrid should contain the word hit and the semantic hit: %+v", hits)
	}

	if _, err := x.Search(Query{Text: "x", Mode: ModeSemantic}); err == nil {
		t.Error("semantic search without a vector must fail")
	}
}

func TestEmbedderSurfacesOllamaErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model \"nope\" not found, try pulling it first"}`))
	}))
	defer srv.Close()
	x := openTemp(t)
	_ = x.ResetFolder("INBOX", 1)
	put(t, x, "INBOX", Doc{UID: 1, Subject: "a", Body: "b"})
	_, err := x.EmbedPending(context.Background(), NewEmbedder(config.Embedding{URL: srv.URL, Model: "nope"}), nil)
	if err == nil || !strings.Contains(err.Error(), "try pulling") {
		t.Errorf("want Ollama's error message, got %v", err)
	}
}
