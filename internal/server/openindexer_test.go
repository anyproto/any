package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
		"chunk target": {refuse: pinOtherChunkTarget},
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

// pinOtherChunkTarget leaves an index db at dbPath that this build
// refuses: it is pinned to another chunk target.
func pinOtherChunkTarget(t *testing.T, dbPath string) {
	t.Helper()
	st, err := indexer.OpenStore(context.Background(), dbPath, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PinChunkRunes(context.Background(), indexer.DefaultChunkRunes+1); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// closeCountEmbedder counts Close calls: the local embedder is one child
// process, so a rebuild must not close and recreate it.
type closeCountEmbedder struct{ closed int }

func (e *closeCountEmbedder) EmbedDocs(context.Context, []string) ([][]float32, error) {
	return nil, nil
}
func (e *closeCountEmbedder) EmbedQuery(context.Context, string) ([]float32, error) { return nil, nil }
func (e *closeCountEmbedder) Dim(context.Context) (int, error)                      { return 0, nil }
func (e *closeCountEmbedder) Close() error                                          { e.closed++; return nil }

// The embedder spans the rebuild: not closed between the refused open
// and the fresh one, and gone with the indexer on Close.
func TestOpenIndexerWith_EmbedderSurvivesRebuild(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	pinOtherChunkTarget(t, filepath.Join(dataDir, "index", "index.db"))

	emb := &closeCountEmbedder{}
	ix, err := openIndexerWith(ctx, config.Defaults().Index, dataDir, emb, nil, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("openIndexerWith over a refused index db: %v, want a rebuild", err)
	}
	if emb.closed != 0 {
		t.Fatalf("embedder closed %d times across the rebuild, want 0", emb.closed)
	}
	if err := ix.Close(); err != nil {
		t.Fatal(err)
	}
	if emb.closed != 1 {
		t.Fatalf("embedder closed %d times after Indexer.Close, want 1", emb.closed)
	}
}

// A rebuild whose removal fails fails the boot with the refusal still
// in the chain (start code 4 on the mobile shims) and closes the embedder.
func TestOpenIndexerWith_FailedRebuildKeepsRefusal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot delete from")
	}
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "index", "index.db")
	pinOtherChunkTarget(t, dbPath)
	// The open needs the WAL and shm files to exist once the dir is
	// read-only: creating them there is what the permission forbids.
	for _, side := range []string{"-wal", "-wal-shm"} {
		f, err := os.OpenFile(dbPath+side, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	indexDir := filepath.Dir(dbPath)
	if err := os.Chmod(indexDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(indexDir, 0o700) })

	emb := &closeCountEmbedder{}
	_, err := openIndexerWith(ctx, config.Defaults().Index, dataDir, emb, nil, nil, 0, nil, nil)
	if !errors.Is(err, indexer.ErrIndexRebuildRequired) {
		t.Fatalf("failed rebuild = %v, want the refusal kept in the chain", err)
	}
	if emb.closed != 1 {
		t.Fatalf("embedder closed %d times after a failed boot, want 1", emb.closed)
	}
}
