package chat

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"
)

// GeneralChatSeed is the fixed derivation seed for a space's single
// "general" chat object — every space has exactly one, derived
// deterministically from this seed. Same convention as the UI's
// primary chat ("any-ui/primary-chat/v1"):
// every peer derives the same object id from the same seed, so there
// is no discovery query and no create race — Objects().Derive is
// idempotent (creates on first call, returns the existing id after).
//
// The motivation is concrete: clients that want "the chat for this
// space" — the common case, and the ONLY case for a 1-1 direct space —
// would otherwise each Objects().Create a fresh chat, so a space ends
// up with two or three parallel chats depending on which client
// spoke first. Deriving from a fixed seed gives every client the same
// well-known id to write to instead.
//
// Versioned: bumping the seed mints a fresh general chat (a clean
// break, not a migration) — only do that together with a chat dataset
// data-version bump.
const GeneralChatSeed = "any/general-chat/v1"

// DeriveGeneralChatObjectId resolves (creating on first use) the
// per-space general chat object that hosts the chat_messages dataset.
// The chat type is attached on first materialization, so writes
// through the dataset handler are admitted without a separate
// ensureType step. Idempotent and deterministic across peers — a
// joiner derives the same id the creator did, so the CRDT-replicated
// chat and the locally derived id converge.
func DeriveGeneralChatObjectId(ctx context.Context, sp space.Space) (string, error) {
	objectId, err := sp.Objects().Derive(ctx, space.DeriveObjectOpts{
		Seed:  []byte(GeneralChatSeed),
		Types: []string{TypeId},
	})
	if err != nil {
		return "", fmt.Errorf("chat: derive general chat object: %w", err)
	}
	return objectId, nil
}
