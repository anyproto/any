//go:build vector

package indexer

import (
	"fmt"

	"github.com/anyproto/any/internal/config"
)

// NewEmbedder constructs the configured embedding client. Returns
// (nil, nil) for "none" — the indexer then runs FTS-only. A bare ""
// also maps to FTS-only: config.Load defaults it to "local", so ""
// only survives when a config file sets it explicitly (the pre-"none"
// opt-out syntax). dataDir hosts the local embedder's downloaded
// model (<dataDir>/index/models).
//
// This is the `vector`-tag variant — the real switch. The !vector
// variant (embed_factory_novector.go) ignores the config and always
// returns nil so the vector pipeline is compiled out entirely.
func NewEmbedder(cfg config.Index, dataDir string) (Embedder, error) {
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
		return NewLocal(cfg.Local, dataDir)
	default:
		return nil, fmt.Errorf("indexer: unknown embedder %q (want \"local\", \"ollama\", \"openai\" or \"none\")", cfg.Embedder)
	}
}
