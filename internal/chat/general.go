package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/bundles"
)

// The per-space "general" chat — every space has exactly one, so
// clients that want "the chat for this space" (the common case, and
// the ONLY case for a 1-1 direct space) share one well-known object
// instead of each creating a parallel chat.
//
// It is an install in the space's bundles registry (any-sync-sdk
// docs/bundles.md): the chat object is the bundle's non-derived root,
// so it is a real deletable object and concurrent first-sights from
// two devices converge on one winner instead of racing. Clients read
// the winner's id off SpaceInfo.generalChatObjectId — never derive it.
const (
	// GeneralChatBundleId is the registry record id. Versioned:
	// a successor general chat takes a new id, it never reuses this
	// one (bundle record ids are permanent).
	GeneralChatBundleId = "general-chat/v1"
	// GeneralChatBundleName is the display name stamped as `any.name`
	// on the chat object when this device installs it.
	GeneralChatBundleName = "General"
)

// GeneralChatInstall describes the general-chat bundle.
//
// Merge is deliberately nil. chat_messages stamp creator/createdAt
// from the change envelope, so copying a loser's messages into the
// winner re-attributes every one of them to the merging device and
// re-times them — worse than leaving them where they are. Instead Keep
// refuses to delete a loser that carries messages: in practice a loser
// is a creation-time race (two devices resolving the space's chat
// before they had synced) and is empty, so the common case resolves
// cleanly and the rare written-to loser survives as a plain object the
// user can still read.
func GeneralChatInstall() bundles.Install {
	return bundles.Install{
		Id:        GeneralChatBundleId,
		Name:      GeneralChatBundleName,
		RootTypes: []string{TypeId},
		Keep:      generalChatHasMessages,
	}
}

// EnsureGeneralChat resolves the space's general chat object,
// installing it on first sight. Idempotent: once a winner is
// registered the call is a local read and writes nothing.
//
// The returned id is provisional until the space syncs — a concurrent
// install on another device can win, at which point this device's root
// surfaces in Bundle.Losers and gets resolved (see
// bundles.ResolveLosers).
func EnsureGeneralChat(ctx context.Context, sp space.Space) (string, error) {
	b, err := bundles.Ensure(ctx, sp, GeneralChatInstall())
	if err != nil {
		return "", fmt.Errorf("chat: ensure general chat: %w", err)
	}
	return b.RootId, nil
}

// generalChatHasMessages reports whether a chat object carries at
// least one live message. Read locally: a loser whose tree has not
// synced yet reads empty, but deleting it needs that same local tree,
// so the SDK refuses the deletion and the check re-runs on the next
// attempt with the synced state.
func generalChatHasMessages(ctx context.Context, sp space.Space, objectId string) (bool, error) {
	_, err := sp.Query(objectId, Dataset).Limit(1).One(ctx)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, space.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("chat: general chat message probe: %w", err)
	}
}
