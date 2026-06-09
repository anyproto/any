package agentmem

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"
)

// BrainSeed is the fixed derivation seed for the per-space brain
// object. Same convention as the UI's primary chat
// ("any-ui/primary-chat/v1"): every peer derives the same object id
// from the same seed, so there is no discovery query and no create
// race — Objects().Derive is idempotent (creates on first call,
// returns the existing id after).
//
// Versioned: bumping the seed mints a fresh brain object (a clean
// break, not a migration) — only do that together with a dataset
// data-version bump.
const BrainSeed = "any/agent-brain/v1"

// DeriveBrainObjectId resolves (creating on first use) the per-space
// brain object that hosts the agent_memory_items dataset. The
// agent_memory type is attached on first materialization, so writes
// through the dataset handler are admitted without a separate
// ensureType step.
func DeriveBrainObjectId(ctx context.Context, sp space.Space) (string, error) {
	objectId, err := sp.Objects().Derive(ctx, space.DeriveObjectOpts{
		Seed:  []byte(BrainSeed),
		Types: []string{TypeId},
	})
	if err != nil {
		return "", fmt.Errorf("agentmem: derive brain object: %w", err)
	}
	return objectId, nil
}
