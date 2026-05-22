package chat

import (
	"errors"
	"fmt"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

// BeforeCreate validates the creation payload, then derives the
// server-stamped row-root fields (creator, createdAt, modifiedAt)
// via sink.Derive so they land alongside the user's text /
// replyToMessageId in one apply step. Chronological order comes from
// the SDK-managed `_ver.id` creation marker — no chat-side stamp
// needed.
//
// The expected creation shape is exactly one multi-field $set op
// (empty Path, object payload). Any other shape rejects the whole
// record — chat creates are aggregating-server-issued, so a
// non-conforming payload is a sign of a buggy peer or a hand-crafted
// change. Per-record reject (errors.Is(ErrValidation)) drops the
// entire RecordChange, not just one op.
//
// Allowed payload keys: text (required, non-empty, ≤ MaxTextBytes),
// replyToMessageId (optional, non-empty, ≤ MaxReplyIdBytes). Any
// other key — including server-stamped ones (creator, createdAt,
// modifiedAt, _co) — rejects, defending against attempts to spoof
// authorship by stuffing fields into the create payload.
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
	return nil
}

// BeforeModify gates per-op edits. Path allow-list:
//
//   - text                                — $set, by author only,
//                                            bumps modifiedAt
//   - reactions.<emoji>.<creator>         — $set (add) or $unset
//                                            (remove), only on the
//                                            caller's own slot
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
		visitErr error
		hasText  bool
	)
	obj.Visit(func(rawKey []byte, v *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldText:
			hasText = true
			if v.Type() != anyenc.TypeString {
				visitErr = rejectCreate("text must be a string")
				return
			}
			text := v.GetStringBytes()
			if len(text) == 0 {
				visitErr = rejectCreate("text required")
				return
			}
			if len(text) > MaxTextBytes {
				visitErr = rejectCreate(fmt.Sprintf("text too long (%d > %d bytes)", len(text), MaxTextBytes))
				return
			}
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
		default:
			visitErr = rejectCreate("field_not_allowed: " + key)
			return
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasText {
		return rejectCreate("text required")
	}
	return nil
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
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldCreatedAt},
			Payload: a.NewNumberInt(int(ts)),
		})
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldModifiedAt},
			Payload: a.NewNumberInt(int(ts)),
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
			Payload: a.NewNumberInt(int(ctx.Change.Timestamp)),
		})
	}
	return nil
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
		op.Payload = a.NewNumberInt(int(ts))
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
