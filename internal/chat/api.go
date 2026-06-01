package chat

import (
	"context"
	"errors"
	"fmt"
	"sort"

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
	if rec, err := sp.Properties().Get(ctx, objectId, space.PropertyReadOpts{}); err == nil && rec != nil {
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
// the handler; ReplyToMessageId is an opaque soft reference;
// FromAgent is an optional opaque tag marking the message as
// agent-authored (UI hint, not verified — see chat.go package doc).
//
// Attachments is an optional create-only client hint. Each
// attachment may carry an optional Order (pointer so absence is
// distinguishable from 0); the handler skips the field when nil.
type SendOpts struct {
	Text             string
	ReplyToMessageId string
	FromAgent        string
	Attachments      map[string]api.ChatAttachment
}

// Send writes one new message and returns the freshly-created record.
//
// The id of the new message is derived from the change CID via the
// SDK's empty-id sugar — base58(xxh3-64(changeId)) — so callers don't
// need to allocate ids client-side. The returned ModifyResult.RecordIds
// carries the resolved id; we read it back so the caller gets the full
// server-stamped record (creator, createdAt, _ver.id, …).
func Send(ctx context.Context, sp space.Space, objectId string, opts SendOpts) (api.ChatMessage, error) {
	if err := ensureType(ctx, sp, objectId); err != nil {
		return api.ChatMessage{}, fmt.Errorf("chat: send: ensure type: %w", err)
	}
	payload := map[string]any{
		FieldText: opts.Text,
	}
	if opts.ReplyToMessageId != "" {
		payload[FieldReplyToMessageId] = opts.ReplyToMessageId
	}
	if opts.FromAgent != "" {
		payload[FieldFromAgent] = opts.FromAgent
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
		return api.ChatMessage{}, fmt.Errorf("chat: send: modify: %w", err)
	}
	if len(res.RecordIds) == 0 {
		return api.ChatMessage{}, fmt.Errorf("chat: send: empty RecordIds")
	}
	if len(res.Rejections) > 0 {
		return api.ChatMessage{}, fmt.Errorf("chat: send: rejected: %s", res.Rejections[0].Reason)
	}
	return Get(ctx, sp, objectId, res.RecordIds[0])
}

// Edit updates the text of an existing message. The author check
// runs both here (clean 403 for local callers) and in the handler
// (defense-in-depth for peer changes).
func Edit(ctx context.Context, sp space.Space, objectId, msgId, callerId, text string) (api.ChatMessage, error) {
	existing, err := Get(ctx, sp, objectId, msgId)
	if err != nil {
		return api.ChatMessage{}, err
	}
	if existing.Creator != callerId {
		return api.ChatMessage{}, ErrNotAuthor
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
		return api.ChatMessage{}, fmt.Errorf("chat: edit: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return api.ChatMessage{}, fmt.Errorf("chat: edit: rejected: %s", res.Rejections[0].Reason)
	}
	return Get(ctx, sp, objectId, msgId)
}

// Delete tombstones a message. Author check runs here for the clean
// 403 path; the handler's BeforeDelete provides peer-side enforcement.
func Delete(ctx context.Context, sp space.Space, objectId, msgId, callerId string) error {
	existing, err := Get(ctx, sp, objectId, msgId)
	if err != nil {
		return err
	}
	if existing.Creator != callerId {
		return ErrNotAuthor
	}
	if _, err := sp.Delete(ctx, space.DeleteBatch{
		ObjectId:  objectId,
		Dataset:   Dataset,
		RecordIds: []string{msgId},
	}); err != nil {
		return fmt.Errorf("chat: delete: %w", err)
	}
	return nil
}

// ToggleReaction adds or removes the caller's emoji from the message
// — read current state, decide $set vs $unset. Storage is
// `reactions.<emoji>.<accountId> = <changeTimestamp>` (emoji first,
// identity at the leaf), so the handler's authorization is a single
// path-segment compare on the leaf; the value is server-derived from
// the change timestamp, so the placeholder we pass here is overwritten
// before it lands.
func ToggleReaction(ctx context.Context, sp space.Space, objectId, msgId, callerId, emoji string) (api.ChatMessage, error) {
	existing, err := getRaw(ctx, sp, objectId, msgId)
	if err != nil {
		return api.ChatMessage{}, err
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
		return api.ChatMessage{}, fmt.Errorf("chat: react: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return api.ChatMessage{}, fmt.Errorf("chat: react: rejected: %s", res.Rejections[0].Reason)
	}
	return Get(ctx, sp, objectId, msgId)
}

// ListOpts is the input to List. Before / After are message ids
// (cursor-style); the server resolves them to the underlying
// `_ver.id` boundary before querying. Limit defaults to 50.
type ListOpts struct {
	Before string
	After  string
	Limit  int
}

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// List returns messages in ascending `_ver.id` order (oldest first),
// optionally bounded by Before / After cursors. Cursors are message
// ids: the server reads the boundary message's `_ver.id` and uses it
// as a `$lt` (Before) or `$gt` (After) filter.
//
// IncludeMeta=true is required because we both sort and filter on
// `_ver.id`, which is hidden by default.
func List(ctx context.Context, sp space.Space, objectId string, opts ListOpts) ([]api.ChatMessage, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	// Build the _ver.id boundary filter with explicit query primitives
	// — map literals run through json.Marshal + anyenc reparse, which
	// is both slower and fragile (a stray non-marshalable value would
	// surface as a parse panic on the terminal call).
	var bounds query.And
	if opts.Before != "" {
		ver, err := readVerId(ctx, sp, objectId, opts.Before)
		if err != nil {
			return nil, err
		}
		bounds = append(bounds, verIdComp(query.CompOpLt, ver))
	}
	if opts.After != "" {
		ver, err := readVerId(ctx, sp, objectId, opts.After)
		if err != nil {
			return nil, err
		}
		bounds = append(bounds, verIdComp(query.CompOpGt, ver))
	}

	q := sp.Query(objectId, Dataset).
		Sort("_ver.id").
		Limit(limit).
		Projection(space.ProjectionOpts{IncludeMeta: true})
	if len(bounds) > 0 {
		q = q.Filter(bounds)
	}

	docs, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("chat: list: query: %w", err)
	}
	out := make([]api.ChatMessage, 0, len(docs))
	for _, d := range docs {
		out = append(out, recordToMessage(d))
	}
	return out, nil
}

// Get fetches one message by id and returns the wire-shaped message.
// Wraps space.ErrNotFound as ErrNotFound so callers can map to 404.
func Get(ctx context.Context, sp space.Space, objectId, msgId string) (api.ChatMessage, error) {
	doc, err := getRaw(ctx, sp, objectId, msgId)
	if err != nil {
		return api.ChatMessage{}, err
	}
	return recordToMessage(doc), nil
}

// getRaw is the shared lookup used by Get / Edit / Delete /
// ToggleReaction. Returns the raw anyenc record so callers can
// inspect server-managed fields (reactions, _ver) without an extra
// round-trip.
func getRaw(ctx context.Context, sp space.Space, objectId, msgId string) (*anyenc.Value, error) {
	doc, err := sp.Query(objectId, Dataset).
		Filter(query.Key{
			Path:   []string{"id"},
			Filter: query.NewComp(query.CompOpEq, msgId),
		}).
		Projection(space.ProjectionOpts{IncludeMeta: true}).
		One(ctx)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("chat: get: %w", err)
	}
	return doc, nil
}

// readVerId resolves a message id to its `_ver.id` value. Used by
// List's pagination cursors. Returns ErrNotFound if the boundary
// message doesn't exist.
func readVerId(ctx context.Context, sp space.Space, objectId, msgId string) (string, error) {
	doc, err := getRaw(ctx, sp, objectId, msgId)
	if err != nil {
		return "", err
	}
	ver := doc.Get("_ver", "id")
	if ver == nil || ver.Type() != anyenc.TypeString {
		return "", fmt.Errorf("chat: readVerId: missing _ver.id on %s", msgId)
	}
	return string(ver.GetStringBytes()), nil
}

// verIdComp builds a Key filter against `_ver.id` for a single
// comparison. Hoisted so List's Before / After branches read as one
// line each.
func verIdComp(op query.CompOp, ver string) query.Filter {
	return query.Key{
		Path:   []string{"_ver", "id"},
		Filter: query.NewComp(op, ver),
	}
}

// identityHasEmoji reports whether `callerId` already has an entry
// for `emoji` in the message's reactions map. Drives the
// add-vs-remove decision in ToggleReaction.
func identityHasEmoji(rec *anyenc.Value, callerId, emoji string) bool {
	leaf := rec.Get(FieldReactions, emoji, callerId)
	return leaf != nil && leaf.Type() == anyenc.TypeNumber
}

// recordToMessage converts a stored anyenc record into the wire
// shape, transposing reactions from identity-keyed (storage) to
// emoji-keyed (wire). The transpose is cheap — reaction maps are
// tiny per message.
//
// modifiedAt is always emitted alongside createdAt; the two are
// equal on never-edited messages. Clients that want a "this message
// has been edited" signal compare the two — suppressing modifiedAt
// when equal would be ambiguous on edits that happen in the same
// second as creation.
func recordToMessage(rec *anyenc.Value) api.ChatMessage {
	return api.ChatMessage{
		Id:               getString(rec, "id"),
		Creator:          getString(rec, FieldCreator),
		CreatedAt:        int64(rec.GetInt(FieldCreatedAt)),
		ModifiedAt:       int64(rec.GetInt(FieldModifiedAt)),
		ReplyToMessageId: getString(rec, FieldReplyToMessageId),
		FromAgent:        getString(rec, FieldFromAgent),
		Text:             getString(rec, FieldText),
		Attachments:      renderAttachments(rec.Get(FieldAttachments)),
		Reactions:        renderReactions(rec.Get(FieldReactions)),
	}
}

// renderAttachments walks the storage shape
// (id → {type, link}) into the wire shape. Nil-safe; returns nil when
// the record has no attachments to keep the JSON output clean
// (omitempty-friendly). Unknown attachment sub-fields are silently
// dropped — clients should only consume `type` and `link`.
func renderAttachments(att *anyenc.Value) map[string]api.ChatAttachment {
	if att == nil || att.Type() != anyenc.TypeObject {
		return nil
	}
	obj, _ := att.Object()
	if obj == nil {
		return nil
	}
	out := map[string]api.ChatAttachment{}
	obj.Visit(func(rawId []byte, v *anyenc.Value) {
		if v == nil || v.Type() != anyenc.TypeObject {
			return
		}
		out[string(rawId)] = api.ChatAttachment{
			Type: getString(v, FieldAttachmentType),
			Link: getString(v, FieldAttachmentLink),
		}
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// renderReactions rolls the storage shape
// (emoji → {accountId: ts}) up to the wire shape (emoji → [accountId,
// ...] sorted by ts ascending so clients display reactions in the
// order they landed). Nil-safe; returns nil when the record has no
// reactions to keep the JSON output clean (omitempty-friendly).
func renderReactions(reactions *anyenc.Value) map[string][]string {
	if reactions == nil || reactions.Type() != anyenc.TypeObject {
		return nil
	}
	obj, _ := reactions.Object()
	if obj == nil {
		return nil
	}
	out := map[string][]string{}
	obj.Visit(func(rawEmoji []byte, perEmoji *anyenc.Value) {
		if perEmoji == nil || perEmoji.Type() != anyenc.TypeObject {
			return
		}
		emoji := string(rawEmoji)
		inner, _ := perEmoji.Object()
		if inner == nil {
			return
		}
		type entry struct {
			id string
			ts int64
		}
		var entries []entry
		inner.Visit(func(rawIdentity []byte, v *anyenc.Value) {
			if v == nil || v.Type() != anyenc.TypeNumber {
				return
			}
			entries = append(entries, entry{
				id: string(rawIdentity),
				ts: int64(v.GetInt()),
			})
		})
		if len(entries) == 0 {
			return
		}
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].ts != entries[j].ts {
				return entries[i].ts < entries[j].ts
			}
			return entries[i].id < entries[j].id
		})
		ids := make([]string, len(entries))
		for i, e := range entries {
			ids[i] = e.id
		}
		out[emoji] = ids
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

func getString(v *anyenc.Value, path ...string) string {
	got := v.Get(path...)
	if got == nil || got.Type() != anyenc.TypeString {
		return ""
	}
	return string(got.GetStringBytes())
}
