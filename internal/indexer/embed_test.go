//go:build vector && !gomobile

package indexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any/internal/config"
)

func TestOllama_EmbedDocs(t *testing.T) {
	var gotBody ollamaEmbedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{1, 2}, {3, 4}}})
	}))
	defer srv.Close()

	c := NewOllama(srv.URL, "test-model")
	vecs, err := c.EmbedDocs(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || vecs[1][1] != 4 {
		t.Fatalf("vecs = %v", vecs)
	}
	if gotBody.Model != "test-model" {
		t.Errorf("model = %q", gotBody.Model)
	}
	// Doc task prompt applied.
	if gotBody.Input[0] != "title: none | text: alpha" {
		t.Errorf("input[0] = %q, doc prompt not applied", gotBody.Input[0])
	}
}

func TestOllama_DimProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{0, 0, 0}}})
	}))
	defer srv.Close()
	dim, err := NewOllama(srv.URL, "m").Dim(context.Background())
	if err != nil || dim != 3 {
		t.Fatalf("dim = %d, %v; want 3", dim, err)
	}
}

func TestOpenAI_EmbedDocs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth = %q", got)
		}
		var req openaiEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "text-embedding-3-small" || len(req.Input) != 2 {
			t.Errorf("req = %+v", req)
		}
		// Out-of-order data — index field is authoritative.
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[3,4]},{"index":0,"embedding":[1,2]}]}`))
	}))
	defer srv.Close()

	c := NewOpenAI(srv.URL, "text-embedding-3-small", "sk-test")
	vecs, err := c.EmbedDocs(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if vecs[0][0] != 1 || vecs[1][0] != 3 {
		t.Fatalf("order not restored from index: %v", vecs)
	}
}

func TestNewEmbedder(t *testing.T) {
	dir := t.TempDir()
	if e, err := NewEmbedder(config.Index{}, dir, ""); e != nil || err != nil {
		t.Errorf("empty embedder should be nil,nil; got %v, %v", e, err)
	}
	if e, err := NewEmbedder(config.Index{Embedder: "ollama"}, dir, ""); err != nil || e == nil {
		t.Errorf("ollama: %v, %v", e, err)
	}
	if _, err := NewEmbedder(config.Index{Embedder: "openai"}, dir, ""); err == nil {
		t.Error("openai without model should error")
	}
	if _, err := NewEmbedder(config.Index{Embedder: "openai", OpenAI: config.IndexOpenAI{Model: "m"}}, dir, ""); err != nil {
		t.Errorf("openai with model: %v", err)
	}
	if _, err := NewEmbedder(config.Index{Embedder: "bogus"}, dir, ""); err == nil {
		t.Error("unknown embedder should error")
	}
}
