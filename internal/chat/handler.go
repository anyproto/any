package chat

import (
	"errors"
	"fmt"
	"slices"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"

	"github.com/anyproto/any/anyuri"
)

// Indexes declares the any-store indexes ensured on each chat
// object's `chat_messages` collection. List sorts by `_ver.id` and
// translates Before/After cursors into `_ver.id` range filters, so
// the index keeps both the order scan and the bound seek off a full
// collection sweep. `_ver.id` is SDK-stamped on every record (set on
// creation, never edited), so the index is dense — no Sparse.
func (messagesHandler) Indexes() []anystore.IndexInfo {
	return []anystore.IndexInfo{
		{Name: "idx_ver_id", Fields: []string{"_ver.id"}},
		// Flag indexes are sparse: the materializer $unsets a flag when
		// it clears, so only currently-unread rows carry the field —
		// the index stays proportional to the unread set.
		{Name: "idx_unread", Fields: []string{FieldUnread, "_ver.id"}, Sparse: true},
		{Name: "idx_unread_mention", Fields: []string{FieldUnreadMention, "_ver.id"}, Sparse: true},
		{Name: "idx_unread_reactions", Fields: []string{FieldUnreadReactions, "_ver.id"}, Sparse: true},
		// mentions is an array field, so the index is multikey (one
		// entry per mentioned identity) — {"mentions": "<identity>"}
		// eq-filters are index-backed. Sparse: the handler $unsets the
		// field when a message mentions nobody.
		{Name: "idx_mentions", Fields: []string{FieldMentions, "_ver.id"}, Sparse: true},
	}
}

// BeforeCreate validates the creation payload, then derives the
// server-stamped row-root fields (creator, createdAt, modifiedAt, and
// the mentions array — see deriveMentions) via sink.Derive so they
// land alongside the user's text / replyToMessageId in one apply
// step. Chronological order comes from the SDK-managed `_ver.id`
// creation marker — no chat-side stamp needed.
//
// The expected creation shape is exactly one multi-field $set op
// (empty Path, object payload). Any other shape rejects the whole
// record — chat creates are aggregating-server-issued, so a
// non-conforming payload is a sign of a buggy peer or a hand-crafted
// change. Per-record reject (errors.Is(ErrValidation)) drops the
// entire RecordChange, not just one op.
//
// Allowed payload keys: text (required, non-empty, ≤ MaxTextBytes),
// replyToMessageId (optional, non-empty, ≤ MaxReplyIdBytes),
// agent (optional {name, debugLink?, done} group — UI hint, not
// verified), attachments (optional), context (optional {spaceId,
// objectId?, view?} — the sender's view at send time). Any other key
// rejects:
//   - server-stamped fields (creator, createdAt, modifiedAt, _co) —
//     defends against spoofing authorship via the create payload.
//   - reactions — a message is always born with zero reactions; the
//     only way to add one is the post-create toggle op (BeforeModify),
//     which binds the leaf to the change signer. Seeding reactions at
//     create would let the author forge reactions under other
//     identities, so the create allow-list omits it.
func (messagesHandler) BeforeCreate(ctx *handler.ChangeCtx, rec *handler.RecordChange, sink *handler.Sink) error {
	if len(rec.Ops) != 1 {
		return rejectCreate("expected exactly one multi-field $set op")
	}
	op := &rec.Ops[0]
	if op.Type != handler.OpSet || len(op.Path) != 0 {
		return rejectCreate("expected multi-field $set with empty path")
	}
	if op.Payload == nil || op.Payload.Type() != anyenc.TypeObject {
		return rejectCreate("payload must be a JSON object")
	}

	if err := validateCreatePayload(op.Payload); err != nil {
		return err
	}

	stampCreate(ctx, sink)
	deriveMentions(ctx, sink,
		op.Payload.GetStringBytes(FieldText),
		string(op.Payload.GetStringBytes(FieldReplyToMessageId)),
		false)
	return nil
}

// BeforeModify gates per-op edits. Path allow-list:
//
//   - text                                — $set, by author only,
//     bumps modifiedAt
//   - reactions.<emoji>.<creator>         — $set (add) or $unset
//     (remove), only on the
//     caller's own slot
//
// Anything else (including direct writes to creator, createdAt,
// modifiedAt, replyToMessageId, _ver.*, _deletedAt) drops the op.
// Other ops in the same RecordChange still apply — per-op rejection
// is the SDK's contract here.
func (messagesHandler) BeforeModify(ctx *handler.ChangeCtx, _ *handler.RecordChange, op *handler.Op, sink *handler.Sink) error {
	switch {
	case isTextEdit(op):
		return validateTextEdit(ctx, op, sink)
	case isReactionToggle(op):
		return validateReactionToggle(ctx, op, sink)
	default:
		return rejectOp("field_not_modifiable: " + pathString(op.Path))
	}
}

// BeforeDelete enforces "you can only delete your own message".
// Rejects the whole record (and therefore the delete) on mismatch.
func (messagesHandler) BeforeDelete(ctx *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Sink) error {
	if !isAuthor(ctx) {
		return rejectRecord("not_author")
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

func validateCreatePayload(payload *anyenc.Value) error {
	obj, err := payload.Object()
	if err != nil {
		return rejectCreate("payload must be a JSON object")
	}

	var (
		visitErr       error
		hasText        bool
		hasAttachments bool
		hasControl     bool
	)
	obj.Visit(func(rawKey []byte, v *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldText:
			if v.Type() != anyenc.TypeString {
				visitErr = rejectCreate("text must be a string")
				return
			}
			text := v.GetStringBytes()
			if len(text) > MaxTextBytes {
				visitErr = rejectCreate(fmt.Sprintf("text too long (%d > %d bytes)", len(text), MaxTextBytes))
				return
			}
			// Emptiness is decided after the visit, not here: whether an
			// empty text is legal depends on `attachments`, which may not
			// have been visited yet.
			hasText = len(text) > 0
		case FieldReplyToMessageId:
			if v.Type() != anyenc.TypeString {
				visitErr = rejectCreate("replyToMessageId must be a string")
				return
			}
			id := v.GetStringBytes()
			if len(id) == 0 {
				visitErr = rejectCreate("replyToMessageId must be non-empty when present")
				return
			}
			if len(id) > MaxReplyIdBytes {
				visitErr = rejectCreate(fmt.Sprintf("replyToMessageId too long (%d > %d bytes)", len(id), MaxReplyIdBytes))
				return
			}
		case FieldAgent:
			if err := validateAgent(v); err != nil {
				visitErr = err
				return
			}
		case FieldAttachments:
			if err := validateAttachments(v); err != nil {
				visitErr = err
				return
			}
			// validateAttachments rejects an empty map, so reaching here
			// means at least one attachment.
			hasAttachments = true
		case FieldContext:
			if err := validateContext(v); err != nil {
				visitErr = err
				return
			}
		case FieldControl:
			if err := validateControl(v); err != nil {
				visitErr = err
				return
			}
			hasControl = true
		default:
			visitErr = rejectCreate("field_not_allowed: " + key)
			return
		}
	})
	if visitErr != nil {
		return visitErr
	}
	// A message needs content: text, attachments, or both. Attachment-only
	// (a photo with no caption) is ordinary; neither is an empty message.
	// A control signal is content of its own — a `break` carries no text.
	if !hasText && !hasAttachments && !hasControl {
		return rejectCreate("text or attachment required")
	}
	return nil
}

// validateContext enforces the structure of the context group — the
// sender's view at send time:
//
//   - must be an object
//   - `spaceId` — required, non-empty string, ≤ MaxContextIdBytes
//   - `objectId` — optional, non-empty string when present, ≤ MaxContextIdBytes
//   - `view` — optional, non-empty string when present, ≤ MaxContextViewBytes
//     (open enum: the client's view kind)
//   - no unknown sub-fields (bump dataVersion when adding any)
func validateContext(v *anyenc.Value) error {
	if v.Type() != anyenc.TypeObject {
		return rejectCreate("context must be an object")
	}
	obj, err := v.Object()
	if err != nil || obj == nil {
		return rejectCreate("context must be an object")
	}
	var (
		visitErr   error
		hasSpaceId bool
	)
	obj.Visit(func(rawKey []byte, val *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		max := MaxContextIdBytes
		switch key {
		case FieldContextSpaceId:
			hasSpaceId = true
		case FieldContextObjectId:
		case FieldContextView:
			max = MaxContextViewBytes
		default:
			visitErr = rejectCreate("context: unknown field " + key)
			return
		}
		if val.Type() != anyenc.TypeString {
			visitErr = rejectCreate("context." + key + " must be a string")
			return
		}
		s := val.GetStringBytes()
		if len(s) == 0 {
			visitErr = rejectCreate("context." + key + " must be non-empty when present")
			return
		}
		if len(s) > max {
			visitErr = rejectCreate(fmt.Sprintf("context.%s too long (%d > %d bytes)", key, len(s), max))
			return
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasSpaceId {
		return rejectCreate("context.spaceId required")
	}
	return nil
}

// validateControl enforces the structure of the control group — a
// client's signal to the agent serving the chat:
//
//   - must be an object
//   - `kind` — required, non-empty string, ≤ MaxControlKindBytes
//     (open enum: `break` asks the run in flight to stop)
//   - `hard` — optional boolean (break: now, vs. at the next turn)
//   - no unknown sub-fields (bump dataVersion when adding any)
func validateControl(v *anyenc.Value) error {
	if v.Type() != anyenc.TypeObject {
		return rejectCreate("control must be an object")
	}
	obj, err := v.Object()
	if err != nil || obj == nil {
		return rejectCreate("control must be an object")
	}
	var (
		visitErr error
		hasKind  bool
	)
	obj.Visit(func(rawKey []byte, val *anyenc.Value) {
		if visitErr != nil {
			return
		}
		switch key := string(rawKey); key {
		case FieldControlKind:
			hasKind = true
			if val.Type() != anyenc.TypeString {
				visitErr = rejectCreate("control.kind must be a string")
				return
			}
			k := val.GetStringBytes()
			if len(k) == 0 {
				visitErr = rejectCreate("control.kind required")
				return
			}
			if len(k) > MaxControlKindBytes {
				visitErr = rejectCreate(fmt.Sprintf("control.kind too long (%d > %d bytes)", len(k), MaxControlKindBytes))
				return
			}
		case FieldControlHard:
			if val.Type() != anyenc.TypeTrue && val.Type() != anyenc.TypeFalse {
				visitErr = rejectCreate("control.hard must be a boolean")
				return
			}
		default:
			visitErr = rejectCreate("control: unknown field " + key)
			return
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasKind {
		return rejectCreate("control.kind required")
	}
	return nil
}

// validateAgent enforces the structure of the agent group:
//
//   - must be an object
//   - `name` — required, non-empty string, ≤ MaxAgentNameBytes
//   - `debugLink` — optional, non-empty string when present,
//     ≤ MaxDebugLinkBytes; opaque to the server (no URL parsing)
//   - `done` — required boolean
//   - `outcome` — optional, non-empty string when present,
//     ≤ MaxAgentOutcomeBytes; opaque to the server (the agent's own
//     vocabulary — `interrupted`, `error`)
//   - no unknown sub-fields (bump dataVersion when adding any)
func validateAgent(v *anyenc.Value) error {
	if v.Type() != anyenc.TypeObject {
		return rejectCreate("agent must be an object")
	}
	obj, err := v.Object()
	if err != nil || obj == nil {
		return rejectCreate("agent must be an object")
	}
	var (
		visitErr         error
		hasName, hasDone bool
	)
	obj.Visit(func(rawKey []byte, val *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldAgentName:
			hasName = true
			if val.Type() != anyenc.TypeString {
				visitErr = rejectCreate("agent.name must be a string")
				return
			}
			n := val.GetStringBytes()
			if len(n) == 0 {
				visitErr = rejectCreate("agent.name required")
				return
			}
			if len(n) > MaxAgentNameBytes {
				visitErr = rejectCreate(fmt.Sprintf("agent.name too long (%d > %d bytes)", len(n), MaxAgentNameBytes))
				return
			}
		case FieldAgentDebugLink:
			if val.Type() != anyenc.TypeString {
				visitErr = rejectCreate("agent.debugLink must be a string")
				return
			}
			l := val.GetStringBytes()
			if len(l) == 0 {
				visitErr = rejectCreate("agent.debugLink must be non-empty when present")
				return
			}
			if len(l) > MaxDebugLinkBytes {
				visitErr = rejectCreate(fmt.Sprintf("agent.debugLink too long (%d > %d bytes)", len(l), MaxDebugLinkBytes))
				return
			}
		case FieldAgentDone:
			hasDone = true
			if val.Type() != anyenc.TypeTrue && val.Type() != anyenc.TypeFalse {
				visitErr = rejectCreate("agent.done must be a boolean")
				return
			}
		case FieldAgentOutcome:
			if val.Type() != anyenc.TypeString {
				visitErr = rejectCreate("agent.outcome must be a string")
				return
			}
			o := val.GetStringBytes()
			if len(o) == 0 {
				visitErr = rejectCreate("agent.outcome must be non-empty when present")
				return
			}
			if len(o) > MaxAgentOutcomeBytes {
				visitErr = rejectCreate(fmt.Sprintf("agent.outcome too long (%d > %d bytes)", len(o), MaxAgentOutcomeBytes))
				return
			}
		default:
			visitErr = rejectCreate("agent: field_not_allowed: " + key)
			return
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasName {
		return rejectCreate("agent.name required")
	}
	if !hasDone {
		return rejectCreate("agent.done required")
	}
	return nil
}

// validateAttachments enforces the structure of the attachments map:
//
//   - must be an object
//   - 1..MaxAttachments entries
//   - each id matches [A-Za-z0-9_-]+ and is ≤ MaxAttachmentIdBytes
//   - each entry is an object with `type` (non-empty string,
//     ≤ MaxAttachmentTypeBytes) and `link` (non-empty string,
//     ≤ MaxAttachmentLinkBytes); both are required
//   - no unknown sub-fields per entry (future-proof: bump dataVersion
//     when adding any new sub-field)
//
// Open enum on `type` — we don't whitelist "link"/"image" here so new
// kinds (e.g. "video", "embed") can flow through without a handler
// bump. Clients render unknown types as plain links per task-chat-
// attachments.md.
func validateAttachments(v *anyenc.Value) error {
	if v.Type() != anyenc.TypeObject {
		return rejectCreate("attachments must be an object")
	}
	obj, err := v.Object()
	if err != nil || obj == nil {
		return rejectCreate("attachments must be an object")
	}
	var (
		visitErr error
		count    int
	)
	obj.Visit(func(rawId []byte, entry *anyenc.Value) {
		if visitErr != nil {
			return
		}
		count++
		if count > MaxAttachments {
			visitErr = rejectCreate(fmt.Sprintf("attachments: too many entries (max %d)", MaxAttachments))
			return
		}
		id := string(rawId)
		if !ValidAttachmentId(id) {
			visitErr = rejectCreate("attachments: invalid id (must match [A-Za-z0-9_-]+, ≤ " +
				fmt.Sprintf("%d", MaxAttachmentIdBytes) + " bytes): " + id)
			return
		}
		if visitErr = validateAttachmentEntry(id, entry); visitErr != nil {
			return
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if count == 0 {
		return rejectCreate("attachments: must be non-empty when present")
	}
	return nil
}

func validateAttachmentEntry(id string, entry *anyenc.Value) error {
	if entry == nil || entry.Type() != anyenc.TypeObject {
		return rejectCreate("attachments[" + id + "]: must be an object")
	}
	obj, err := entry.Object()
	if err != nil || obj == nil {
		return rejectCreate("attachments[" + id + "]: must be an object")
	}
	var (
		visitErr        error
		hasType, hasLnk bool
	)
	obj.Visit(func(rawKey []byte, val *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldAttachmentType:
			hasType = true
			if val.Type() != anyenc.TypeString {
				visitErr = rejectCreate("attachments[" + id + "].type must be a string")
				return
			}
			t := val.GetStringBytes()
			if len(t) == 0 {
				visitErr = rejectCreate("attachments[" + id + "].type required")
				return
			}
			if len(t) > MaxAttachmentTypeBytes {
				visitErr = rejectCreate(fmt.Sprintf("attachments[%s].type too long (%d > %d bytes)",
					id, len(t), MaxAttachmentTypeBytes))
				return
			}
		case FieldAttachmentLink:
			hasLnk = true
			if val.Type() != anyenc.TypeString {
				visitErr = rejectCreate("attachments[" + id + "].link must be a string")
				return
			}
			l := val.GetStringBytes()
			if len(l) == 0 {
				visitErr = rejectCreate("attachments[" + id + "].link required")
				return
			}
			if len(l) > MaxAttachmentLinkBytes {
				visitErr = rejectCreate(fmt.Sprintf("attachments[%s].link too long (%d > %d bytes)",
					id, len(l), MaxAttachmentLinkBytes))
				return
			}
		default:
			visitErr = rejectCreate("attachments[" + id + "]: unknown field " + key)
			return
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasType {
		return rejectCreate("attachments[" + id + "].type required")
	}
	if !hasLnk {
		return rejectCreate("attachments[" + id + "].link required")
	}
	return nil
}

// ValidAttachmentId reports whether id is a legal attachment key:
// non-empty, ≤ MaxAttachmentIdBytes, alphabet [A-Za-z0-9_-]. Shared by
// the handler-side validation and the HTTP layer's pre-flight check.
func ValidAttachmentId(id string) bool {
	if len(id) == 0 || len(id) > MaxAttachmentIdBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

// stampCreate queues sink.Derive ops for the row-root server-stamped
// fields. Same VersionId as the user's $set, so concurrent peer-side
// stamps converge under standard LWW.
func stampCreate(ctx *handler.ChangeCtx, sink *handler.Sink) {
	if ctx == nil || ctx.Change == nil || sink == nil {
		return
	}
	a := &anyenc.Arena{}
	if c := ctx.Change.Creator; c != "" {
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldCreator},
			Payload: a.NewString(c),
		})
	}
	if ts := ctx.Change.Timestamp; ts > 0 {
		// Instants, not epoch numbers — the shape any-store orders,
		// indexes and computes dates on. The envelope carries unix
		// SECONDS; a dateTime value is millis.
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldCreatedAt},
			Payload: a.NewDateTimeMillis(ts * 1000),
		})
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldModifiedAt},
			Payload: a.NewDateTimeMillis(ts * 1000),
		})
	}
}

func isTextEdit(op *handler.Op) bool {
	return op.Type == handler.OpSet &&
		len(op.Path) == 1 &&
		op.Path[0] == FieldText
}

func validateTextEdit(ctx *handler.ChangeCtx, op *handler.Op, sink *handler.Sink) error {
	if !isAuthor(ctx) {
		return rejectOp("not_author")
	}
	if op.Payload == nil || op.Payload.Type() != anyenc.TypeString {
		return rejectOp("text must be a string")
	}
	text := op.Payload.GetStringBytes()
	if len(text) == 0 {
		return rejectOp("text required")
	}
	if len(text) > MaxTextBytes {
		return rejectOp(fmt.Sprintf("text too long (%d > %d bytes)", len(text), MaxTextBytes))
	}
	if ctx != nil && ctx.Change != nil && ctx.Change.Timestamp > 0 {
		a := &anyenc.Arena{}
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldModifiedAt},
			Payload: a.NewDateTimeMillis(ctx.Change.Timestamp * 1000),
		})
	}
	// Re-derive mentions from the new text. replyToMessageId is
	// create-only, so the pre-op record is authoritative for the reply
	// fold-in.
	var replyTo string
	if ctx != nil && ctx.Before != nil {
		replyTo = string(ctx.Before.GetStringBytes(FieldReplyToMessageId))
	}
	deriveMentions(ctx, sink, text, replyTo, true)
	return nil
}

// deriveMentions computes and stamps the derived `mentions` array: the
// identities mentioned in text (any://m/… links, deduped in
// first-occurrence order) plus, for replies, the replied-to message's
// creator — read via ctx.Get, which is replica-deterministic here
// because `creator` is an immutable create-stamp and the replied-to
// create is a causal ancestor of this change. Accepted edge: a reply
// racing a concurrent DELETE of its target reads a creator-less
// tombstone on replicas that applied the delete first, so their
// derived array omits the fold-in — display-only divergence (derived
// ops are local re-derivation, never synced payload).
//
// A message with no mentions carries no field: create stamps nothing,
// an edit $unsets (unconditionally — a no-op when already absent), so
// the sparse idx_mentions holds only mentioning rows.
func deriveMentions(ctx *handler.ChangeCtx, sink *handler.Sink, text []byte, replyTo string, isEdit bool) {
	if sink == nil {
		return
	}
	// Text mentions are capped BEFORE the reply fold-in so the fold-in
	// can never be the entry truncation drops: the replied-to author is
	// the one recipient the reply contract guarantees a ping for, and a
	// link-stuffed reply must not squeeze them out (that would be the
	// notification-suppression vector this field exists to close). When
	// the cap is hit, the last text mention yields the slot instead.
	ids := anyuri.ExtractMentions(string(text))
	if len(ids) > MaxMentions {
		ids = ids[:MaxMentions]
	}
	if replyTo != "" && ctx != nil && ctx.Get != nil {
		if rec := ctx.Get(Dataset, replyTo); rec != nil && rec.Get("_deletedAt") == nil {
			if creator := string(rec.GetStringBytes(FieldCreator)); creator != "" && !slices.Contains(ids, creator) {
				if len(ids) == MaxMentions {
					ids = ids[:MaxMentions-1]
				}
				ids = append(ids, creator)
			}
		}
	}
	if len(ids) == 0 {
		if isEdit {
			sink.Derive(handler.Op{Type: handler.OpUnset, Path: []string{FieldMentions}})
		}
		return
	}
	a := &anyenc.Arena{}
	arr := a.NewArray()
	for i, id := range ids {
		arr.SetArrayItem(i, a.NewString(id))
	}
	sink.Derive(handler.Op{Type: handler.OpSet, Path: []string{FieldMentions}, Payload: arr})
}

func isReactionToggle(op *handler.Op) bool {
	if op.Type != handler.OpSet && op.Type != handler.OpUnset {
		return false
	}
	return len(op.Path) == 3 &&
		op.Path[0] == FieldReactions &&
		op.Path[1] != "" &&
		op.Path[2] != ""
}

// validateReactionToggle gates a leaf write on
// `reactions.<emoji>.<accountId>`. The third path segment IS the user
// identity, so authorization is one string compare — anyone can
// toggle their own slot; nobody can toggle someone else's.
//
// For $set the handler overwrites the op's payload in place with
// ctx.Change.Timestamp, so the client's placeholder value never
// lands. It deliberately does NOT emit a separate derived op: a
// derived op targets the same leaf path with the same VersionId, and
// the CRDT version gate drops the second writer at an equal version
// (the user op stamps _ver for the path first, so the derived op's
// `_ver >= version` check short-circuits). Rewriting the user op's
// payload keeps a single writer. For $unset there's nothing to
// stamp — the leaf is gone.
func validateReactionToggle(ctx *handler.ChangeCtx, op *handler.Op, _ *handler.Sink) error {
	if ctx == nil || ctx.Change == nil {
		return rejectOp("missing change context")
	}
	emoji := op.Path[1]
	if len(emoji) > MaxEmojiBytes {
		return rejectOp(fmt.Sprintf("emoji too long (%d > %d bytes)", len(emoji), MaxEmojiBytes))
	}
	if op.Path[2] != ctx.Change.Creator {
		return rejectOp("not_own_reaction_key")
	}
	if op.Type == handler.OpSet {
		ts := ctx.Change.Timestamp
		if ts <= 0 {
			return rejectOp("missing change timestamp")
		}
		a := &anyenc.Arena{}
		op.Payload = a.NewDateTimeMillis(ts * 1000)
	}
	return nil
}

// isAuthor compares the existing creator on the record with the
// change's signer. Returns false if either is empty — handlers must
// fail-closed when the SDK hasn't stamped Creator (pre-existing
// records or hand-built test changes).
func isAuthor(ctx *handler.ChangeCtx) bool {
	if ctx == nil || ctx.Change == nil || ctx.Before == nil {
		return false
	}
	signer := ctx.Change.Creator
	if signer == "" {
		return false
	}
	creator := ctx.Before.Get(FieldCreator)
	if creator == nil || creator.Type() != anyenc.TypeString {
		return false
	}
	return string(creator.GetStringBytes()) == signer
}

func pathString(path []string) string {
	if len(path) == 0 {
		return "<root>"
	}
	out := path[0]
	for _, seg := range path[1:] {
		out += "." + seg
	}
	return out
}

func rejectCreate(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("chat.create: %s", msg))
}

func rejectRecord(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("chat: %s", msg))
}

func rejectOp(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("chat: %s", msg))
}
