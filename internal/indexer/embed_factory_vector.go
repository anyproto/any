//go:build vector && !gomobile

package indexer

import (
	"fmt"

	"github.com/anyproto/any/internal/config"
)

// NewEmbedder constructs the configured embedding client. Returns
// (nil, nil) for "none" — the indexer then runs FTS-only. A bare ""
// also maps to FTS-only: config.Load defaults it to "local", so ""
// only survives when a config file sets it explicitly (the pre-"none"
// opt-out syntax). modelsDir hosts the local embedder's downloaded
// model (shared across accounts, <root>/models); legacyModelsDir is the
// pre-per-account location (<dataDir>/index/models), used instead when
// the model file already exists there.
//
// This is the `vector && !gomobile` variant — the real switch. The
// counterpart (embed_factory_novector.go, `!vector || gomobile`) ignores
// the config and always returns nil so the vector pipeline is compiled
// out entirely — always the case on mobile.
func NewEmbedder(cfg config.Index, modelsDir, legacyModelsDir string) (Embedder, error) {
	switch cfg.Embedder {
	case "", "none":
		return nil, nil
	case "ollama":
		return NewOllama(cfg.Ollama.Url, cfg.Ollama.Model), nil
	case "openai":
		if cfg.OpenAI.Model == "" {
			return nil, fmt.Errorf("indexer: openai embedder needs index.openai.model")
		}
		return NewOpenAI(cfg.OpenAI.BaseUrl, cfg.OpenAI.Model, cfg.OpenAI.ApiKey), nil
	case "local":
		return NewLocal(cfg.Local, modelsDir, legacyModelsDir)
	default:
		return nil, fmt.Errorf("indexer: unknown embedder %q (want \"local\", \"ollama\", \"openai\" or \"none\")", cfg.Embedder)
	}
}
