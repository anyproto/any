//go:build mobile

package indexer

import (
	"context"
	"errors"

	"github.com/anyproto/any/internal/config"
)

// This file is the `mobile`-build counterpart of embed_local.go. The
// real local embedder dlopens llama.cpp through the yzma/purego
// bindings, which cannot be cross-compiled into the iOS c-archive
// (IOS-6169). Under `-tags mobile` those dependencies are compiled out
// entirely and `index.embedder: local` is unsupported — callers must
// use "none" (FTS-only), "ollama", or "openai".
//
// Local mirrors the real type's NAME and Embedder-method set ONLY so the
// `case "local"` branch in embed.go type-checks identically across both
// builds (NewLocal returns *Local there). It is never instantiated: the
// embed.go default branch calls NewLocal, which always errors before any
// *Local value is produced.
type Local struct{}

// NewLocal matches the !mobile signature exactly and FAILS LOUD. It
// returns a genuine nil *Local with a non-nil error so the embed.go
// `case "local": return NewLocal(...)` path propagates the error and
// never boxes a value into the Embedder interface.
//
// Returning (nil, error) — not a non-nil *Local — is deliberate: a
// typed-nil *Local boxed into the Embedder interface would be a NON-nil
// interface (Go's typed-nil trap), so HasEmbedder() would report true
// and the vector pipeline would later panic dereferencing it. The error
// short-circuits NewEmbedder before that can happen.
func NewLocal(cfg config.IndexLocal, modelsDir, legacyModelsDir string) (*Local, error) {
	return nil, errors.New("indexer: local embedder not available in mobile build (use index.embedder \"none\", \"ollama\", or \"openai\")")
}

// The Embedder-interface methods below exist only to keep *Local
// assignable to Embedder under this build tag, identical to the real
// type. They are unreachable (NewLocal never returns a non-nil *Local)
// and fail loud if ever called.
var errMobileLocalUnavailable = errors.New("indexer: local embedder not available in mobile build")

func (l *Local) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, errMobileLocalUnavailable
}

func (l *Local) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return nil, errMobileLocalUnavailable
}

func (l *Local) Dim(ctx context.Context) (int, error) {
	return 0, errMobileLocalUnavailable
}

func (l *Local) Close() error { return nil }
