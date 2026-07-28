// Package agentsecrets registers the built-in `agent_secrets` type — the
// per-space agent secrets object. One object per space, derived from a
// fixed seed (same mechanic as the config object, the brain object and
// the general chat: Objects().Derive is idempotent and deterministic
// across peers, so there is no discovery query and no create race). It
// hosts the `agent_secrets` dataset: one record per secret ref
// (`connector.key.linear`, …).
//
// Secrets used to live in `agent_config` as `{key, secret: true,
// localValue}` records, mixed in with plain config overrides. They move
// to their own object so the anybao runtime can block guest reads of the
// WHOLE dataset by name/id — one meaningful 403-style error at the
// effect boundary — while `agent_config` stays guest-readable.
// `agent_config` is unchanged and the two coexist: config overrides
// resolve against the config object, secrets against this one.
//
// Record shape (owned by the harness, as with agent_config): record id =
// the secret ref; synced `key` carries the same ref string plus a
// `secret: true` marker; device-local `value` (ScopeLocal, never synced)
// carries the secret itself. Storage is a raw DefaultHandler dataset:
// the server just materializes the object and reports its id on the
// single-space GET (spaceToAPI → SpaceInfo.AgentSecretsObjectId), the
// same delivery path as agentConfigObjectId.
package agentsecrets

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/handler"
	"github.com/anyproto/any-sync-sdk/space"
)

const (
	TypeId      = "agent_secrets"
	Name        = "Agent Secrets"
	Description = "Per-space agent secrets (device-local values keyed by secret ref)"

	// Dataset holds one record per secret ref.
	Dataset = "agent_secrets"

	// Record field names. `key` is the secret ref (same string as the
	// record id — synced so every peer sees WHICH secrets exist);
	// `secret` is the constant true marker mirroring the agent_config
	// convention; `value` is the secret payload — device-local only,
	// never synced.
	FieldKey    = "key"
	FieldSecret = "secret"
	FieldValue  = "value"

	// SecretsObjectSeed is the fixed derivation seed for the per-space
	// secrets object. Versioned: bumping it mints a fresh secrets object
	// (a clean break, not a migration) — only together with a dataset
	// data-version bump.
	SecretsObjectSeed = "any/agent-secrets/v1"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). One DefaultHandler dataset carrying one
// record per secret ref. The keyspace stays Dynamic (undeclared dotted
// keys remain permitted, synced) — the schema only pins the field
// CLASSES the secret contract depends on: `key`/`secret` synced,
// `value` ScopeLocal (device-only, never synced). Declaring value
// local-scope is what makes the secret payload writable — an undeclared
// field on a Dynamic dataset defaults to synced, and the apply path
// rejects a local write to a synced field (same mechanic as
// agent_config's localValue).
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
						{Id: FieldSecret, Name: "Secret", Schema: handler.Leaf(handler.PropertyKindBoolean), Scope: handler.ScopeSynced},
						{Id: FieldValue, Name: "Value", Scope: handler.ScopeLocal},
					},
				},
			},
		},
	}
}

// DeriveSecretsObjectId resolves (creating on first use) the per-space
// secrets object that hosts the agent_secrets dataset. The agent_secrets
// type is attached on first materialization, so writes through the
// dataset handler are admitted without a separate ensureType step.
// Idempotent and deterministic across peers.
func DeriveSecretsObjectId(ctx context.Context, sp space.Space) (string, error) {
	objectId, err := sp.Objects().Derive(ctx, space.DeriveObjectOpts{
		Seed:  []byte(SecretsObjectSeed),
		Types: []string{TypeId},
	})
	if err != nil {
		return "", fmt.Errorf("agentsecrets: derive secrets object: %w", err)
	}
	return objectId, nil
}
