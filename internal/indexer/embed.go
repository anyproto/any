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
// embedder is configured but not currently reachable (retryable), or
// it answers at another dimension than the index (until a rebuild).
// Vector-mode searches surface it; hybrid degrades to FTS instead.
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

// EmbedStatus is what an embed drain reports beside its counters
// (docs/22-processes.md § Internal producers).
type EmbedStatus struct {
	Phase string // EmbedPhase*
	// Backends are the runtimes that embedded the last batch the
	// embedder returned (EmbedBackend*); nil before the first one, and
	// when the embedder does not say.
	Backends []string
	// Reason is one cause for texts of that batch running off the
	// fastest runtime (EmbedReason*); "" when none did.
	Reason string
}

const (
	EmbedPhaseWaiting   = "waiting"   // the start delay runs
	EmbedPhasePreparing = "preparing" // the embedder readies a runtime
	EmbedPhaseEmbedding = "embedding"

	EmbedBackendANE    = "ane"    // Core ML on the Neural Engine
	EmbedBackendGPU    = "gpu"    // llama.cpp that reported layers offloaded to a GPU
	EmbedBackendCPU    = "cpu"    // llama.cpp without offload
	EmbedBackendLlama  = "llama"  // llama.cpp, offload asked for and not established
	EmbedBackendRemote = "remote" // an HTTP embedder

	EmbedReasonFunctionLoading  = "function_loading"  // the Neural Engine function for the text's length was not loaded
	EmbedReasonTooLong          = "too_long"          // longer than every function
	EmbedReasonANEFailed        = "ane_failed"        // a function failed to load or to predict
	EmbedReasonANEOff           = "ane_off"           // the Neural Engine path is off for this run
	EmbedReasonReadinessTimeout = "readiness_timeout" // bulk work stopped waiting for the first function
	EmbedReasonGPUFailed        = "gpu_failed"        // GPU offload was demoted after a child died
	EmbedReasonGPUDisabled      = "gpu_disabled"      // index.local.gpuLayers is 0
)

// EmbedRoute is which runtimes embedded one batch, and why not the
// fastest one.
type EmbedRoute struct {
	Backends []string
	Reason   string
}

// RoutedEmbedder is an Embedder that knows which runtimes embedded a
// batch. titles is nil when documents embed without their names.
type RoutedEmbedder interface {
	EmbedDocsRouted(ctx context.Context, titles, texts []string) ([][]float32, EmbedRoute, error)
}

// embedBackends is the runtime of an embedder that has only one.
func embedBackends(e Embedder) []string {
	if b, ok := e.(interface{ EmbedBackend() string }); ok {
		return []string{b.EmbedBackend()}
	}
	return nil
}

// StampedEmbedder names the vector space it embeds into, so an index
// built by another one is rebuilt instead of mixing vectors.
type StampedEmbedder interface {
	// Stamp identifies the model with its prompts and post-processing;
	// "" is unknown. legacy reports whether an index written before
	// stamps existed may hold this embedder's vectors.
	Stamp() (id string, legacy bool)
}

// EmbedderStamp is e's stamp; an embedder without one (an unnamed GGUF)
// is unknown and may own any unstamped index.
func EmbedderStamp(e Embedder) (id string, legacy bool) {
	if s, ok := e.(StampedEmbedder); ok {
		return s.Stamp()
	}
	return "", true
}

// EmbedderStampTwins lists the other stamps whose vector space is e's:
// an index one of them built is e's own.
func EmbedderStampTwins(e Embedder) []string {
	if t, ok := e.(interface{ StampTwins() []string }); ok {
		return t.StampTwins()
	}
	return nil
}

// TitleEmbedder is an Embedder whose document prompt can carry the name
// of the object a text belongs to.
type TitleEmbedder interface {
	Embedder
	// EmbedsTitles reports whether the name is part of what a document
	// embeds as; when it is, a rename makes the vectors of the object's
	// editor windows stale.
	EmbedsTitles() bool
	// EmbedTitledDocs is EmbedDocs with titles[i] the name texts[i]
	// embeds under ("" for none).
	EmbedTitledDocs(ctx context.Context, titles, texts []string) ([][]float32, error)
}

// embedsTitles reports whether e puts object names into document prompts.
func embedsTitles(e Embedder) bool {
	te, ok := e.(TitleEmbedder)
	return ok && te.EmbedsTitles()
}

// titlePlaceholder in a document prompt is replaced by the object's name.
const titlePlaceholder = "{title}"

// maxTitleRunes bounds the name a document prompt carries.
const maxTitleRunes = 200

// docPrompt fills a document prompt's title placeholder: the name on one
// line, clamped, or "none".
func docPrompt(prefix, title string) string {
	if !strings.Contains(prefix, titlePlaceholder) {
		return prefix
	}
	title = strings.Join(strings.Fields(title), " ")
	if r := []rune(title); len(r) > maxTitleRunes {
		title = string(r[:maxTitleRunes])
	}
	if title == "" {
		title = "none"
	}
	return strings.Replace(prefix, titlePlaceholder, title, 1)
}

// Hardware is what the embedder actually runs on, as llama.cpp reports
// it: the backends it registered, the devices they found, and the libs
// they came from. The child collects it once at startup and hands it to
// the server in the `ready` frame, which logs it and keeps it — the
// input for correlating speed and crashes with hardware later.
type Hardware struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	CPUs      int    `json:"cpus"`
	Threads   int    `json:"threads"`
	GpuLayers int    `json:"gpuLayers"`
	// Offloaded is how many layers llama.cpp reported on a GPU when it
	// loaded the model; nil = it did not say.
	Offloaded  *int     `json:"offloaded,omitempty"`
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
