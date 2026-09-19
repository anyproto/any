package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

// TestOpenIndexer_RebuildRequiredClosesStore: a refused index db is
// closed before OpenIndexer returns, so the rebuild the error advises
// (removing the index dir) works while the process lives, and the next
// boot can open the db again.
func TestOpenIndexer_RebuildRequiredClosesStore(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "index", "index.db")

	st, err := indexer.OpenStore(ctx, dbPath, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PinChunkRunes(ctx, indexer.DefaultChunkRunes+1); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults().Index
	cfg.Embedder = "none"
	_, err = OpenIndexer(ctx, cfg, dataDir, t.TempDir(), nil, nil, nil, nil)
	if !errors.Is(err, indexer.ErrIndexRebuildRequired) {
		t.Fatalf("OpenIndexer over another chunk target: %v, want ErrIndexRebuildRequired", err)
	}

	st, err = indexer.OpenStore(ctx, dbPath, 0, false)
	if err != nil {
		t.Fatalf("index db still open after the refusal: %v", err)
	}
	_ = st.Close()
}
