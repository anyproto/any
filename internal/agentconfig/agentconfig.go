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
// as SpaceIndexObjectId.
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

	// Record field names (anybao docs/adr 006 §3 cascade:
	// localValue ?? value ?? default). `key` is the dotted config key;
	// `value` is the space-scope override (synced across peers); `secret`
	// marks a key whose value is device-local only (the config helper
	// refuses synced writes to it); `localValue` is the device-local
	// override — never synced, the top of the cascade (e.g. the Anthropic
	// API key persisted on first serve start).
	FieldKey        = "key"
	FieldValue      = "value"
	FieldSecret     = "secret"
	FieldLocalValue = "localValue"

	// ConfigObjectSeed is the fixed derivation seed for the per-space
	// config object. Versioned: bumping it mints a fresh config object
	// (a clean break, not a migration) — only together with a dataset
	// data-version bump.
	ConfigObjectSeed = "any/agent-config/v1"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). One DefaultHandler dataset carrying one record
// per dotted config key. The keyspace stays Dynamic (undeclared dotted
// keys remain permitted, synced) — the schema only pins the field CLASSES
// the cascade depends on: `value` synced (space override), `localValue`
// ScopeLocal (device-only, never synced). Declaring localValue local-scope
// is what makes device-local secret persistence writable — an undeclared
// field on a Dynamic dataset defaults to synced, and the apply path
// rejects a local write to a synced field. DataVersion stays "1": adding
// these declarations is additive (existing {key,value} synced records stay
// valid), so no older writer is rejected and the config object is not
// re-minted.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{
				Name:        Dataset,
				DataVersion: "1",
				Handler:     handler.DefaultHandler{},
				Schema: handler.Schema{
					Dynamic: true,
					Fields: []handler.Field{
						{Id: FieldKey, Name: "Key", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
						{Id: FieldValue, Name: "Value", Scope: handler.ScopeSynced},
						{Id: FieldSecret, Name: "Secret", Schema: handler.Leaf(handler.PropertyKindBoolean), Scope: handler.ScopeSynced},
						{Id: FieldLocalValue, Name: "Local Value", Scope: handler.ScopeLocal},
					},
				},
			},
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
