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
// is a creation-time race (two of the account's devices resolving the
// space's chat before they had synced) and is empty, so the common
// case resolves cleanly and the rare written-to loser survives as a
// plain object the user can still read.
func GeneralChatInstall() bundles.Install {
	return bundles.Install{
		Id:            GeneralChatBundleId,
		Name:          GeneralChatBundleName,
		RootTypes:     []string{TypeId},
		SoleInstaller: generalChatInstaller,
		Keep:          generalChatHasMessages,
	}
}

// generalChatInstaller names the one member allowed to install a
// space's general chat. Two members installing before they have seen
// each other's registry row would split the conversation across two
// chat objects, and chat messages cannot be merged back together.
//
// An ordinary space has an owner, and the owner is that member. A 1-1
// has none — its ACL owner is a synthetic key nobody holds — so the
// pair picks by comparing the two account identities: both sides know
// both (SpaceInfo.Author carries the peer on a 1-1) and both compute
// the same answer without exchanging anything.
func generalChatInstaller(info space.SpaceInfo, accountId string) bool {
	if accountId == "" {
		return false
	}
	if info.SpaceType == space.SpaceTypeOneToOne {
		return info.Author != "" && accountId < info.Author
	}
	// Author is the resolved ACL owner, OwnRole the mirrored own
	// permission. Either alone answers "am I the owner", and each has
	// a state where it is not populated yet, so both are consulted.
	return info.OwnRole == space.PermissionOwner || info.Author == accountId
}

// generalChatHasMessages reports whether a chat object carries at
// least one live message. The read is local, and ErrNotFound covers
// both "no messages" and "nothing projected here yet" — so it is only
// a verdict once the object has stopped receiving changes, which is
// what the resolver's quiescence gate establishes before calling it.
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
