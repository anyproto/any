package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

// An index db this build cannot open — pinned to another chunk target,
// built on another vector dimension, or failing its integrity check on
// a dirty open — is removed and rebuilt by OpenIndexer instead of
// failing the boot. Everything named after the db goes with it; the
// model cache beside it stays.
func TestOpenIndexer_RebuildsRefusedIndex(t *testing.T) {
	ctx := context.Background()

	for name, tc := range map[string]struct {
		refuse func(t *testing.T, dbPath string)
		dim    int // index.vector.dim for the boot under test
	}{
		"chunk target": {
			refuse: func(t *testing.T, dbPath string) {
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
			},
		},
		"vector dim": {
			refuse: func(t *testing.T, dbPath string) {
				st, err := indexer.OpenStore(ctx, dbPath, 768, true)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
			},
			dim: 1024,
		},
		"integrity check": {
			refuse: func(t *testing.T, dbPath string) {
				st, err := indexer.OpenStore(ctx, dbPath, 0, false)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				tearStorePage2(t, dbPath)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			dbPath := filepath.Join(dataDir, "index", "index.db")
			tc.refuse(t, dbPath)
			sidecar := dbPath + ".marker"
			model := filepath.Join(dataDir, "index", "models", "keep")
			for _, p := range []string{sidecar, model} {
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			cfg := config.Defaults().Index
			cfg.Embedder = "none"
			cfg.Vector.Dim = tc.dim
			ix, err := OpenIndexer(ctx, cfg, dataDir, t.TempDir(), nil, nil, nil, nil)
			if err != nil {
				t.Fatalf("OpenIndexer over a refused index db: %v, want a rebuild", err)
			}
			if err := ix.Close(); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
				t.Fatalf("a file named after the index db survived the rebuild (stat err %v)", err)
			}
			if _, err := os.Stat(model); err != nil {
				t.Fatalf("model cache removed with the index db: %v", err)
			}
			// The rebuilt db carries this boot's pins, not the refused one's.
			st, err := indexer.OpenStore(ctx, dbPath, 0, false)
			if err != nil {
				t.Fatalf("reopen the rebuilt index db: %v", err)
			}
			defer st.Close()
			if err := st.PinChunkRunes(ctx, indexer.DefaultChunkRunes); err != nil {
				t.Fatalf("rebuilt db still carries the old chunk target: %v", err)
			}
			if tc.dim != 0 && st.Dim() != tc.dim {
				t.Fatalf("rebuilt db dim = %d, want %d", st.Dim(), tc.dim)
			}
		})
	}
}

// tearStorePage2 damages a closed any-store v2 db the way a torn write
// does and leaves the dirty sentinel beside it, so the next open runs
// the quick check and fails it: page 2 is the system namespace root the
// check reads, and the page checksum catches the flipped bytes.
func tearStorePage2(t *testing.T, dbPath string) {
	t.Helper()
	b, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	const pageSize = 4096
	if len(b) < 2*pageSize {
		t.Fatalf("db is %d bytes, want at least two pages", len(b))
	}
	for i := pageSize + 100; i < pageSize+600; i++ {
		b[i] ^= 0xff
	}
	if err := os.WriteFile(dbPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
}
