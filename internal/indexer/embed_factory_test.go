package indexer

import (
	"strings"
	"testing"

	"github.com/anyproto/any/internal/config"
)

// The online primary has a twin only for Qwen3: a key with another
// local model is refused instead of mixing two vector spaces.
func TestNewEmbedder_AutoRefusesKeyWithAnotherLocalModel(t *testing.T) {
	cfg := config.Index{Embedder: "auto",
		OpenAI: config.IndexOpenAI{BaseUrl: "http://127.0.0.1:1", Model: "Qwen/Qwen3-Embedding-0.6B", ApiKey: "k"},
		Local:  config.IndexLocal{Model: config.LocalModelEmbeddingGemma2},
	}
	emb, err := NewEmbedder(cfg, t.TempDir(), "", nil)
	if !localEmbedderBuilt {
		// No local leg to mismatch: the primary runs alone.
		if err != nil || emb == nil {
			t.Fatalf("a build without the local embedder: %v, %v", emb, err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "index.local.model") {
		t.Fatalf("err = %v, want a refusal naming index.local.model", err)
	}
}
