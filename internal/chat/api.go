package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// ErrNotFound signals that a referenced messageId does not exist on
// the chat object's `chat_messages` dataset. Distinct from
// space.ErrNotFound so callers can map cleanly to 404 chat.not_found.
var ErrNotFound = errors.New("chat: message not found")

// ErrNotAuthor signals an edit / delete / react attempt by someone
// other than the original message's creator. Maps to 403
// chat.not_author at the HTTP layer.
var ErrNotAuthor = errors.New("chat: not the message author")

// ensureType attaches the chat type to the object's any.types if not
// already present, so the membership-gated chat_messages write is
// admitted by the SDK. Idempotent and cheap: a local read, then
// AttachType only on first use (subsequent sends find it present).
func ensureType(ctx context.Context, sp space.Space, objectId string) error {
	if rec, err := sp.Properties().Get(ctx, objectId); err == nil && rec != nil {
		for _, v := range rec.GetArray("any", "types") {
			if string(v.GetStringBytes()) == TypeId {
				return nil
			}
		}
	}
	_, err := sp.Properties().AttachType(ctx, objectId, TypeId)
	return err
}

// SendOpts is the input to Send. Text is required and validated by
// the handler; ReplyToMessageId is an opaque soft reference; Agent is
// an optional group marking the message as agent-authored (UI hint,
// not verified — see chat.go package doc).
//
// Attachments is an optional create-only client hint; each entry is
// {type, link}.
type SendOpts struct {
	Text             string
	ReplyToMessageId string
	Agent            *api.ChatAgentMeta
	Attachments      map[string]api.ChatAttachment
}

// Send writes one new message and returns the raw space.ModifyResult.
// The message id is derived from the change CID via the SDK's empty-id
// sugar — base58(xxh3-64(changeId)) — surfaced as res.RecordIds[0]; the
// versionId/changeId let the caller correlate the write with the live
// event it'll receive over /query/subscribe. The full record is read
// back through /query, never re-rendered here.
func Send(ctx context.Context, sp space.Space, objectId string, opts SendOpts) (space.ModifyResult, error) {
	if err := ensureType(ctx, sp, objectId); err != nil {
		return space.ModifyResult{}, fmt.Errorf("chat: send: ensure type: %w", err)
	}
	payload := map[string]any{
		FieldText: opts.Text,
	}
	if opts.ReplyToMessageId != "" {
		payload[FieldReplyToMessageId] = opts.ReplyToMessageId
	}
	if opts.Agent != nil {
		agent := map[string]any{
			FieldAgentName: opts.Agent.Name,
			FieldAgentDone: opts.Agent.Done,
		}
		if opts.Agent.DebugLink != "" {
			agent[FieldAgentDebugLink] = opts.Agent.DebugLink
		}
		payload[FieldAgent] = agent
	}
	if len(opts.Attachments) > 0 {
		atts := make(map[string]any, len(opts.Attachments))
		for id, a := range opts.Attachments {
			atts[id] = map[string]any{
				FieldAttachmentType: a.Type,
				FieldAttachmentLink: a.Link,
			}
		}
		payload[FieldAttachments] = atts
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Id:     "",
			Upsert: true,
			Ops: []space.Op{{
				Type:  space.OpSet,
				Path:  "",
				Value: payload,
			}},
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("chat: send: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("chat: send: rejected: %s", res.Rejections[0].Reason)
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, fmt.Errorf("chat: send: empty RecordIds")
	}
	return res, nil
}

// Edit updates the text of an existing message and returns the
// space.ModifyResult. The author check runs both here (clean 403 for
// local callers) and in the handler (defense-in-depth for peer changes).
func Edit(ctx context.Context, sp space.Space, objectId, msgId, callerId, text string) (space.ModifyResult, error) {
	if err := requireAuthor(ctx, sp, objectId, msgId, callerId); err != nil {
		return space.ModifyResult{}, err
	}
	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Id: msgId,
			Ops: []space.Op{{
				Type:  space.OpSet,
				Path:  FieldText,
				Value: text,
			}},
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("chat: edit: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("chat: edit: rejected: %s", res.Rejections[0].Reason)
	}
	return res, nil
}

// Delete tombstones a message and returns the space.ModifyResult.
// Author check runs here for the clean 403 path; the handler's
// BeforeDelete provides peer-side enforcement.
func Delete(ctx context.Context, sp space.Space, objectId, msgId, callerId string) (space.ModifyResult, error) {
	if err := requireAuthor(ctx, sp, objectId, msgId, callerId); err != nil {
		return space.ModifyResult{}, err
	}
	res, err := sp.Delete(ctx, space.DeleteBatch{
		ObjectId:  objectId,
		Dataset:   Dataset,
		RecordIds: []string{msgId},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("chat: delete: %w", err)
	}
	return res, nil
}

// ToggleReaction adds or removes the caller's emoji from the message
// — read current state, decide $set vs $unset — and returns the
// space.ModifyResult. Storage is `reactions.<emoji>.<accountId> =
// <changeTimestamp>` (emoji first, identity at the leaf), so the
// handler's authorization is a single path-segment compare on the
// leaf; the value is server-derived from the change timestamp, so the
// placeholder we pass here is overwritten before it lands.
func ToggleReaction(ctx context.Context, sp space.Space, objectId, msgId, callerId, emoji string) (space.ModifyResult, error) {
	existing, err := getRaw(ctx, sp, objectId, msgId)
	if err != nil {
		return space.ModifyResult{}, err
	}
	path := FieldReactions + "." + emoji + "." + callerId

	var op space.Op
	if identityHasEmoji(existing, callerId, emoji) {
		op = space.Op{Type: space.OpUnset, Path: path}
	} else {
		// Value is a placeholder; chat's BeforeModify overwrites the
		// op payload with ctx.Change.Timestamp before it lands.
		op = space.Op{Type: space.OpSet, Path: path, Value: 0}
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Id:  msgId,
			Ops: []space.Op{op},
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("chat: react: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("chat: react: rejected: %s", res.Rejections[0].Reason)
	}
	return res, nil
}

// requireAuthor reads the message's creator and returns ErrNotAuthor
// when it doesn't match callerId (ErrNotFound when the message is
// absent). Gives edit/delete a clean local 403; the handler re-checks
// for peer-originated changes.
func requireAuthor(ctx context.Context, sp space.Space, objectId, msgId, callerId string) error {
	rec, err := getRaw(ctx, sp, objectId, msgId)
	if err != nil {
		return err
	}
	if getString(rec, FieldCreator) != callerId {
		return ErrNotAuthor
	}
	return nil
}

// getRaw is the shared lookup used by requireAuthor / ToggleReaction.
// Returns the raw anyenc record so callers can inspect server-managed
// fields (creator, reactions) without an extra round-trip.
func getRaw(ctx context.Context, sp space.Space, objectId, msgId string) (*anyenc.Value, error) {
	doc, err := sp.Query(objectId, Dataset).
		Filter(query.Key{
			Path:   []string{"id"},
			Filter: query.NewComp(query.CompOpEq, msgId),
		}).
		One(ctx)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("chat: get: %w", err)
	}
	return doc, nil
}

// identityHasEmoji reports whether `callerId` already has an entry
// for `emoji` in the message's reactions map. Drives the
// add-vs-remove decision in ToggleReaction.
func identityHasEmoji(rec *anyenc.Value, callerId, emoji string) bool {
	leaf := rec.Get(FieldReactions, emoji, callerId)
	return leaf != nil && leaf.Type() == anyenc.TypeNumber
}

func getString(v *anyenc.Value, path ...string) string {
	got := v.Get(path...)
	if got == nil || got.Type() != anyenc.TypeString {
		return ""
	}
	return string(got.GetStringBytes())
}
