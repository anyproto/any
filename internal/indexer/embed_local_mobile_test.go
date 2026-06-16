//go:build mobile

package indexer

import (
	"testing"

	"github.com/anyproto/any/internal/config"
)

// TestNewEmbedder_LocalUnavailableOnMobile is the IOS-6169 spike-0.1 guard:
// under -tags mobile the local embedder is compiled out, so the
// `case "local"` branch must FAIL LOUD — a non-nil error and a true-nil
// Embedder, never a typed-nil *Local boxed into the interface (which would
// make HasEmbedder() report true and panic the vector pipeline).
func TestNewEmbedder_LocalUnavailableOnMobile(t *testing.T) {
	emb, err := NewEmbedder(config.Index{Embedder: "local"}, t.TempDir(), "")
	if err == nil {
		t.Fatal(`NewEmbedder("local") must error on the mobile build`)
	}
	if emb != nil {
		t.Fatalf(`NewEmbedder("local") must return a true-nil Embedder on error, got %T`, emb)
	}

	// NewLocal directly: same contract — (nil, error), never a non-nil *Local.
	l, err := NewLocal(config.IndexLocal{}, t.TempDir(), "")
	if err == nil {
		t.Fatal("NewLocal must error on the mobile build")
	}
	if l != nil {
		t.Fatalf("NewLocal must return a nil *Local on the mobile build, got %v", l)
	}
}
