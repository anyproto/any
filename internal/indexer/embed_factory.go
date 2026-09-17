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
		if cfg.OpenAI.Model == "" {
			return nil, fmt.Errorf("indexer: openai embedder needs index.openai.model")
		}
		return NewOpenAI(cfg.OpenAI.BaseUrl, cfg.OpenAI.Model, cfg.OpenAI.ApiKey), nil
	case "local":
		return newLocalEmbedder(cfg.Local, modelsDir, legacyModelsDir, onProcess)
	case "auto":
		// Graceful degradation: prefer the online OpenAI-compatible primary
		// (fast, batched), fall back to the always-downloaded local model on
		// an outage. Both MUST be the same model (index.openai.model must
		// name the same model the local embedder runs) — see fallbackEmbedder.
		// A build without the local embedder runs the primary alone.
		if cfg.OpenAI.Model == "" {
			return nil, fmt.Errorf("indexer: auto embedder needs index.openai.model (the online primary, same model as local)")
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
