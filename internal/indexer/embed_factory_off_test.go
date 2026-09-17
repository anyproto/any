//go:build !llamacpp || android || ios

package indexer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/config"
)

func TestNewEmbedder_WithoutLocal(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models")
	if _, err := NewEmbedder(config.Index{Embedder: "local"}, models, "", nil); !errors.Is(err, errLocalNotBuilt) {
		t.Errorf("local: got %v, want errLocalNotBuilt", err)
	}
	e, err := NewEmbedder(config.Index{Embedder: "auto", OpenAI: config.IndexOpenAI{Model: "m"}}, models, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.(*OpenAI); !ok {
		t.Errorf("auto: want the online primary alone, got %T", e)
	}
	if _, err := os.Stat(models); !os.IsNotExist(err) {
		t.Errorf("auto must not start a model download: stat %s: %v", models, err)
	}
}
