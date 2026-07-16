package chat

import (
	"context"
	"slices"

	"github.com/anyproto/any-store/v2/query"
	"github.com/anyproto/any-sync-sdk/handler"
	"github.com/anyproto/any-sync-sdk/space"
)

// Read tracking. The SDK maintains the unread set per chat object
// (see the sdk's docs/read-tracking-proposal.md); chat declares WHAT
// counts as unread via classifyRead below, and where it materializes:
//
//   - per-message local flags: unread / unreadMention /
//     unreadReactions — filterable, ride the normal query/subscribe
//     flow ({"unread": true} finds unread messages anywhere in
//     history, including mid-history arrivals);
//   - per-chat local counters on the object's row: unreadCount /
//     unreadMentions / unreadReactionsCount — the chat-list badges.
//
// Read state is private to the account: synced across its devices
// through the tech space, never visible to other members. Marking is
// forward-only (no mark-unread).

// Read-tracking tags (SDK unread-entry labels).
const (
	TagMessage  = "message"
	TagMention  = "mention"
	TagReaction = "reaction"
)

// Per-message local flag fields (declared ScopeLocal in the schema).
const (
	FieldUnread          = "unread"
	FieldUnreadMention   = "unreadMention"
	FieldUnreadReactions = "unreadReactions"
)

// Per-chat counter properties on the object's row (declared
// ScopeLocal in NewType().Properties).
const (
	PropUnreadCount          = "unreadCount"
	PropUnreadMentions       = "unreadMentions"
	PropUnreadReactionsCount = "unreadReactionsCount"
)

// classifyRead is the ReadTracking classifier: one verdict per applied
// record change.
//
//   - New messages track as "message". (Mention detection lands with
//     the mentions feature; until then unreadMention never sets.)
//   - Reaction toggles track as "reaction" with a supersede key, so a
//     reaction removed before anyone saw it leaves nothing behind —
//     and an un-react clears the pending unread reaction. The verdict
//     is audience-restricted to the reacted-to message's AUTHOR: a
//     reaction is a signal to the person who wrote the message, so it
//     badges only them — someone reacting to a third party's message
//     never lights your counter (audienceAuthor below).
//   - Edits and deletes are untracked: an edit never re-flags a
//     message, and the SDK clears a deleted record's unread entries
//     itself.
func classifyRead(ctx *handler.ChangeCtx, rec *handler.RecordChange) handler.ReadClassification {
	for i := range rec.Ops {
		op := &rec.Ops[i]
		switch op.Type {
		case handler.OpDelete:
			return handler.ReadClassification{}
		case handler.OpSet, handler.OpUnset:
			// reactions.<emoji>.<accountId> — a reaction toggle. The
			// record id is always explicit here (toggles target an
			// existing message), so it's safe in the supersede key.
			if len(op.Path) == 3 && op.Path[0] == FieldReactions {
				key := "reaction:" + op.Path[1] + ":" + op.Path[2] + ":" + rec.Id
				if op.Type == handler.OpSet {
					return handler.ReadClassification{
						Track:    true,
						Tags:     []string{TagReaction},
						Key:      key,
						Audience: audienceAuthor(ctx.SelfIdentity),
					}
				}
				return handler.ReadClassification{Key: key}
			}
		}
	}
	if rec.Upsert {
		return handler.ReadClassification{Track: true, Tags: []string{TagMessage}}
	}
	return handler.ReadClassification{}
}

// audienceAuthor is the audience filter "this replica's account wrote
// the target message" — the SDK matches it against the reacted-to
// record, so the entry tracks only on the author's own replicas.
// `creator` is a derived create-stamp (immutable), which is what makes
// the verdict replay-deterministic. Self identity is per-process, not
// per-package, so the filter is built per verdict — reaction toggles
// are rare enough that this never shows up.
func audienceAuthor(self string) query.Filter {
	return query.Key{Path: []string{FieldCreator},
		Filter: query.NewComp(query.CompOpEq, self)}
}

// readTracking is the registration attached to the chat_messages
// dataset in NewType.
func readTracking() *handler.ReadTracking {
	return &handler.ReadTracking{
		Classify: classifyRead,
		Seed:     handler.ReadSeedAtFirstSight,
		CounterFields: map[string]string{
			TagMessage:  PropUnreadCount,
			TagMention:  PropUnreadMentions,
			TagReaction: PropUnreadReactionsCount,
		},
		RecordFlags: map[string]string{
			TagMessage:  FieldUnread,
			TagMention:  FieldUnreadMention,
			TagReaction: FieldUnreadReactions,
		},
	}
}

// ReadAll marks every unread change in the chat read (messages,
// mentions, reactions) and publishes the account's read position to
// its other devices.
func ReadAll(ctx context.Context, sp space.Space, objectId string) error {
	return sp.ReadState().MarkReadUpTo(ctx, objectId, "")
}

// Read marks msgId's message and everything ordered before it read —
// "read up to here" in the chat's display order (`_ver.id`). A later
// unread change targeting an older message (a fresh reaction on a
// message above the line) stays unread: the user hasn't seen it.
func Read(ctx context.Context, sp space.Space, objectId, msgId string) error {
	rec, err := getRaw(ctx, sp, objectId, msgId)
	if err != nil {
		return err
	}
	verId := string(rec.GetStringBytes("_ver", "id"))
	if verId == "" {
		return ErrNotFound
	}
	return sp.ReadState().MarkReadUpTo(ctx, objectId, space.VersionId(verId))
}

// ReadReactions marks the unread REACTION changes on msgId read. A
// reaction is a separate change written after its target message, so
// Read(msgId) — which cuts at the message's own _ver.id — never covers
// it; a client that has shown the reaction to the user clears it here
// via MarkRead on the reaction change ids.
//
// SCOPE (important): MarkRead covers the given changes AND their causal
// ancestry, so this also marks read any unread MESSAGE the reactor had
// already seen when they reacted — everything causally before the
// reaction, not just the reaction itself. Messages that arrived AFTER
// the reaction stay unread (they aren't ancestors), which is what still
// separates this from ReadAll. In the target case — a reaction on an
// already-read message — the ancestry holds no unread rows, so only the
// reaction clears; when unread messages coexist, the client is expected
// to have marked the visible ones read (viewport /read) first. The SDK
// exposes no "mark exactly these rows, no ancestry walk" primitive
// (MarkRead is ancestry-covering, MarkReadUpTo is range-covering).
// Idempotent: a message with no unread reactions is a no-op (204, not 404).
func ReadReactions(ctx context.Context, sp space.Space, objectId, msgId string) error {
	snapshot, _, err := sp.ReadState().UnreadSnapshot(ctx, objectId)
	if err != nil {
		return err
	}
	var changeIds []string
	for _, ch := range snapshot {
		if slices.Contains(ch.Tags, TagReaction) && slices.Contains(ch.RecordIds, msgId) {
			changeIds = append(changeIds, ch.ChangeId)
		}
	}
	if len(changeIds) == 0 {
		return nil
	}
	return sp.ReadState().MarkRead(ctx, objectId, changeIds)
}
