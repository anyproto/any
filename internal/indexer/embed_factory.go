package indexer

import (
	"errors"
	"fmt"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/config"
)

// errLocalNotBuilt is newLocalEmbedder's answer in a build without the
// local llama.cpp embedder (embed_local_off.go): no `llamacpp` tag, or a
// mobile target.
var errLocalNotBuilt = errors.New(`indexer: index.embedder "local" needs a build with -tags llamacpp (docs/13-index.md § Builds and the local embedder)`)

// NewEmbedder constructs the configured embedding client. Returns
// (nil, nil) for "none" — the indexer then runs FTS-only. A bare ""
// also maps to FTS-only: config.Load defaults it to "auto", so ""
// only survives when a config file sets it explicitly (the pre-"none"
// opt-out syntax). modelsDir hosts the local embedder's downloaded
// model (shared across accounts, <root>/models); legacyModelsDir is the
// pre-per-account location (<dataDir>/index/models), used instead when
// the model file already exists there.
func NewEmbedder(cfg config.Index, modelsDir, legacyModelsDir string, onProcess func(ProcessUpdate)) (Embedder, error) {
	switch cfg.Embedder {
	case "", "none":
		return nil, nil
	case "ollama":
		return NewOllama(cfg.Ollama.Url, cfg.Ollama.Model), nil
	case "openai":
		if cfg.OpenAI.BaseUrl == "" || cfg.OpenAI.Model == "" {
			return nil, fmt.Errorf("indexer: openai embedder needs index.openai.baseUrl and index.openai.model")
		}
		return NewOpenAI(cfg.OpenAI.BaseUrl, cfg.OpenAI.Model, cfg.OpenAI.ApiKey), nil
	case "local":
		return newLocalEmbedder(cfg.Local, modelsDir, legacyModelsDir, onProcess)
	case "auto":
		// Graceful degradation: prefer the online OpenAI-compatible primary
		// (fast, batched), fall back to the always-downloaded local model on
		// an outage. Both MUST be the same model (index.openai.model must
		// name the same model the local embedder runs) — see fallbackEmbedder.
		//
		// Without an API key there is no primary: the text stays on the
		// device instead of being posted to a host that will refuse it. A
		// build without the local embedder has neither leg then, so the
		// index runs FTS-only rather than refusing the default config.
		if cfg.OpenAI.ApiKey == "" {
			e, err := newLocalEmbedder(cfg.Local, modelsDir, legacyModelsDir, onProcess)
			if errors.Is(err, errLocalNotBuilt) {
				logger.NewNamed("indexer.embedder").Warn("auto embedder: no online provider (index.openai.apiKey) and no local embedder in this build (-tags llamacpp) — the index runs full-text only")
				return nil, nil
			}
			return e, err
		}
		if cfg.OpenAI.BaseUrl == "" || cfg.OpenAI.Model == "" {
			return nil, fmt.Errorf("indexer: auto embedder with an apiKey needs index.openai.baseUrl and index.openai.model (the online primary, same model as local)")
		}
		primary := NewOpenAI(cfg.OpenAI.BaseUrl, cfg.OpenAI.Model, cfg.OpenAI.ApiKey)
		fallback, err := newLocalEmbedder(cfg.Local, modelsDir, legacyModelsDir, onProcess)
		if errors.Is(err, errLocalNotBuilt) {
			logger.NewNamed("indexer.embedder").Info("auto embedder runs the online primary alone: this build has no local embedder (-tags llamacpp)")
			return primary, nil
		}
		if err != nil {
			return nil, err
		}
		return newFallbackEmbedder(primary, fallback), nil
	default:
		return nil, fmt.Errorf("indexer: unknown embedder %q (want \"local\", \"auto\", \"ollama\", \"openai\" or \"none\")", cfg.Embedder)
	}
}
