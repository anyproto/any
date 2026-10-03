package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// index.db runs with the dirty sentinel: a write creates index.db.lock
// and a clean close removes it.
func TestOpenStore_SentinelClearsOnClose(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := OpenStore(ctx, path, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCursor(ctx, "space", 1, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("sentinel after write: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("sentinel after close: err=%v", err)
	}
}
