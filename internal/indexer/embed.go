// Package indexer is the phase-2 consumer of the chunker contract in
// internal/index (docs/13-index.md): a background service that keeps a
// local any-store database with a BM25 full-text index and an IVF-SQ
// vector index per space, fed from the SDK's per-space change feed
// (Space.Changes()), and a hybrid (RRF) search over both.
package indexer

import (
	"context"
	"errors"
	"strings"
)

// ErrEmbedderUnavailable wraps query-time embedding failures: the
// embedder is configured but not currently reachable. Vector-mode
// searches surface it as a retryable condition; hybrid degrades to FTS
// instead.
var ErrEmbedderUnavailable = errors.New("indexer: embedder unavailable")

// Embedder turns text into vectors. A nil Embedder on the Indexer means
// FTS-only operation: documents are stored without vectors and the
// vector search leg is unavailable.
//
// Documents and queries embed through separate methods because retrieval
// models distinguish the two roles (task prompts / instructions); both
// must produce vectors of the same dimension.
type Embedder interface {
	// EmbedDocs embeds passages for storage, one vector per text. On
	// error it may return the vectors of a leading prefix of texts
	// alongside the error; the caller lands those and retries the rest.
	EmbedDocs(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQuery embeds a single search query.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	// Dim reports the embedding dimension (probing the backend if needed).
	Dim(ctx context.Context) (int, error)
}

// NewEmbedder constructs the configured embedding client. Its two
// build-tagged variants live in embed_factory_vector.go (the real
// switch) and embed_factory_novector.go (a no-op returning nil, so the
// indexer runs FTS-only when the `vector` tag is absent or the build is
// gomobile). See docs/13-index.md § build tags.

// Hardware is what the embedder actually runs on, as llama.cpp reports
// it: the backends it registered, the devices they found, and the libs
// they came from. The child collects it once at startup and hands it to
// the server in the `ready` frame, which logs it and keeps it — the
// input for correlating speed and crashes with hardware later.
type Hardware struct {
	OS         string   `json:"os"`
	Arch       string   `json:"arch"`
	CPUs       int      `json:"cpus"`
	Threads    int      `json:"threads"`
	GpuLayers  int      `json:"gpuLayers"`
	LibDir     string   `json:"libDir,omitempty"`
	LibVersion string   `json:"libVersion,omitempty"` // llama.cpp release + platform, from the libs' VERSION stamp
	Backends   []string `json:"backends,omitempty"`   // "Vulkan from libggml-vulkan.so"
	Devices    []string `json:"devices,omitempty"`    // "AMD ... (RADV RAPHAEL_MENDOCINO) (radv) | uma: 1 | ..."
	SystemInfo string   `json:"systemInfo,omitempty"` // llama_print_system_info()
}

// String renders the one-line form used in logs.
func (h Hardware) String() string {
	parts := []string{h.OS + "/" + h.Arch}
	if h.LibVersion != "" {
		parts = append(parts, "llama.cpp "+h.LibVersion)
	}
	if len(h.Backends) > 0 {
		parts = append(parts, strings.Join(h.Backends, ", "))
	}
	if len(h.Devices) > 0 {
		parts = append(parts, strings.Join(h.Devices, "; "))
	}
	return strings.Join(parts, " | ")
}
