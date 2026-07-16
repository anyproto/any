// Package agentconfig registers the built-in `agent_config` type — the
// per-space agent configuration object (anybao docs/adr 006 §3). One
// object per space, derived from a fixed seed (same mechanic as the
// brain object and the general chat: Objects().Derive is idempotent and
// deterministic across peers, so there is no discovery query and no
// create race). It hosts the `agent_config` dataset: one record per
// dotted config key (`llm.tier.codegen`, `any.base_url`, …).
//
// The harness (anybao) resolves config against this object with a
// cascade — space-scope overrides shadow the harness's hardcoded
// defaults; the API key and other secrets stay device-local and never
// synced. Storage is a raw DefaultHandler dataset: the harness owns the
// record shape and the resolution policy, the server just materializes
// the object and reports its id on the single-space GET
// (spaceToAPI → SpaceInfo.AgentConfigObjectId), the same delivery path
// as generalChatObjectId.
package agentconfig

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/handler"
	"github.com/anyproto/any-sync-sdk/space"
)

const (
	TypeId      = "agent_config"
	Name        = "Agent Config"
	Description = "Per-space agent configuration (dotted config keys + overrides)"

	// Dataset holds one record per dotted config key.
	Dataset = "agent_config"

	// ConfigObjectSeed is the fixed derivation seed for the per-space
	// config object. Versioned: bumping it mints a fresh config object
	// (a clean break, not a migration) — only together with a dataset
	// data-version bump.
	ConfigObjectSeed = "any/agent-config/v1"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). One DefaultHandler dataset; no declared
// properties — the record values carry the config key/value/scope.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: Dataset, DataVersion: "1", Handler: handler.DefaultHandler{}},
		},
	}
}

// DeriveConfigObjectId resolves (creating on first use) the per-space
// config object that hosts the agent_config dataset. The agent_config
// type is attached on first materialization, so writes through the
// dataset handler are admitted without a separate ensureType step.
// Idempotent and deterministic across peers.
func DeriveConfigObjectId(ctx context.Context, sp space.Space) (string, error) {
	objectId, err := sp.Objects().Derive(ctx, space.DeriveObjectOpts{
		Seed:  []byte(ConfigObjectSeed),
		Types: []string{TypeId},
	})
	if err != nil {
		return "", fmt.Errorf("agentconfig: derive config object: %w", err)
	}
	return objectId, nil
}
