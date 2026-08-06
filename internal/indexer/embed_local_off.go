//go:build vector && !gomobile && (mobile || nolocalembed)

package indexer

import (
	"fmt"

	"github.com/anyproto/any/internal/config"
)

// This variant compiles the vector leg WITHOUT the in-process local
// embedder. The `nolocalembed` tag cuts the yzma → jupiterrider/ffi
// import edge, whose package-level init dlopens an ad-hoc-signed
// libffi.8.dylib before main — macOS library validation (App Sandbox,
// or hardened runtime without the disable-library-validation
// entitlement) denies that load and the process panics before main.
// Same load-time-crash class as the mobile carve-outs (docs/13-index.md
// § build tags), which is why `mobile` shares this variant.
const hasLocalEmbedder = false

// NewLocal here rejects `index.embedder: local` at construction — a
// boot-time error, since a runtime toggle cannot prevent a load-time
// crash. "auto" never reaches this: the factory checks hasLocalEmbedder
// and runs the online primary alone.
func NewLocal(config.IndexLocal, string, string) (Embedder, error) {
	return nil, fmt.Errorf("indexer: the in-process local embedder is not compiled into this build — use \"openai\", \"ollama\", \"auto\" (online-only here) or \"none\"")
}
