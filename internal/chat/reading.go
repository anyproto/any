package chat

import (
	"context"

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
//   - New messages track as "message", plus "mention" when the derived
//     `mentions` array (stamped by the handler in the same apply, read
//     back post-apply via mentionsSelf) contains this replica's
//     account. The create entry is deliberately KEYLESS: a keyed entry
//     is clearable by a later same-key verdict, and no edit must ever
//     be able to clear the create's "message" unread.
//   - Text edits track as "mention" (only) when the re-derived
//     mentions include self — an edit that pings you re-notifies; one
//     that stops pinging you clears the edit-tracked entry via the
//     supersede key. Because the key collapses repeats, N re-edits of
//     a mentioning message hold ONE live mention entry. The `unread` /
//     "message" semantics of edits are unchanged (never re-flagged).
//   - Reaction toggles track as "reaction" with a supersede key, so a
//     reaction removed before anyone saw it leaves nothing behind —
//     and an un-react clears the pending unread reaction. The verdict
//     is audience-restricted to the reacted-to message's AUTHOR: a
//     reaction is a signal to the person who wrote the message, so it
//     badges only them — someone reacting to a third party's message
//     never lights your counter (audienceAuthor below).
//   - Deletes are untracked: the SDK clears a deleted record's unread
//     entries itself.
//
// Self-mentions and self-replies never badge — self-authored changes
// are born read account-wide regardless of the verdict.
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
			// text — an edit. Real creates carry a multi-field $set
			// (empty path); the Upsert guard keeps any create shape on
			// the create branch below. The record id is explicit on
			// edits, so it's safe in the supersede key.
			if !rec.Upsert && op.Type == handler.OpSet && len(op.Path) == 1 && op.Path[0] == FieldText {
				key := "mention:" + rec.Id
				if mentionsSelf(ctx) {
					return handler.ReadClassification{Track: true, Tags: []string{TagMention}, Key: key}
				}
				return handler.ReadClassification{Key: key}
			}
		}
	}
	if rec.Upsert {
		tags := []string{TagMessage}
		if mentionsSelf(ctx) {
			tags = append(tags, TagMention)
		}
		return handler.ReadClassification{Track: true, Tags: tags}
	}
	return handler.ReadClassification{}
}

// mentionsSelf reports whether the just-applied record's derived
// `mentions` array contains this replica's account. Classification
// runs after the record loop in the same tx, so ctx.Get sees the
// array the handler stamped for this very change — one read covers
// text mentions AND the reply fold-in, with no duplicate parsing. An
// Audience filter can't express this: it gates the WHOLE verdict, and
// on creates the mention tag rides next to the unconditional
// "message" tag. Verdicts are device-local, so keying off
// SelfIdentity here is sound.
func mentionsSelf(ctx *handler.ChangeCtx) bool {
	if ctx == nil || ctx.Get == nil || ctx.SelfIdentity == "" || ctx.RecordId == "" {
		return false
	}
	rec := ctx.Get(Dataset, ctx.RecordId)
	if rec == nil {
		return false
	}
	for _, v := range rec.GetArray(FieldMentions) {
		if string(v.GetStringBytes()) == ctx.SelfIdentity {
			return true
		}
	}
	return false
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
