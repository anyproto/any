package email

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/ensure"
)

// ErrNotFound signals that a referenced message id does not exist on
// the mailbox object's `email_messages` dataset. Maps to 404
// email.not_found at the HTTP layer.
var ErrNotFound = errors.New("email: message not found")

// ErrNotAuthor signals a patch / delete attempt by someone other than
// the ingesting account. Maps to 403 email.not_author.
var ErrNotAuthor = errors.New("email: not the message author")

// ErrNoFields signals a patch request with no mutable field present.
var ErrNoFields = errors.New("email: no fields to patch")

// Ingest upserts one sync page (1..MaxIngestBatch messages) onto the
// object's email_messages dataset in ONE ModifyBatch. Per message id:
//
//   - absent  → created with the full payload (BeforeCreate validates,
//     stamps creator/createdAt/modifiedAt, derives participants)
//   - present → the mutable fields (labelIds, historyId) are compared
//     against the stored record; changed ones land as per-field $set
//     (BeforeModify allow-list), identical ones are skipped entirely
//
// Immutable fields of an existing record are never compared or
// rewritten — a provider message's content doesn't change, only its
// label state does. Handler-refused records surface in
// Result.Rejections and are excluded from the outcome lists. When
// nothing needs writing, no change is committed (empty versionId).
func Ingest(ctx context.Context, sp space.Space, objectId string, msgs []api.EmailMessage) (api.EmailIngestResult, error) {
	out := api.EmailIngestResult{
		Created:   []string{},
		Updated:   []string{},
		Unchanged: []string{},
	}
	if err := ensure.TypeAttached(ctx, sp, objectId, TypeId); err != nil {
		return out, fmt.Errorf("email: ingest: ensure type: %w", err)
	}

	existing, err := fetchExisting(ctx, sp, objectId, msgs)
	if err != nil {
		return out, err
	}

	var (
		records   []space.RecordModify
		recordIds []string // parallel to records, for rejection mapping
	)
	for i := range msgs {
		msg := &msgs[i]
		stored, ok := existing[msg.Id]
		if !ok {
			records = append(records, space.RecordModify{
				Id:     msg.Id,
				Upsert: true,
				Ops: []space.Op{{
					Type:  space.OpSet,
					Path:  "",
					Value: createPayload(msg),
				}},
			})
			recordIds = append(recordIds, msg.Id)
			out.Created = append(out.Created, msg.Id)
			continue
		}
		ops := mutableDiffOps(stored, msg)
		if len(ops) == 0 {
			out.Unchanged = append(out.Unchanged, msg.Id)
			continue
		}
		records = append(records, space.RecordModify{Id: msg.Id, Ops: ops})
		recordIds = append(recordIds, msg.Id)
		out.Updated = append(out.Updated, msg.Id)
	}
	if len(records) == 0 {
		return out, nil
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records:  records,
	})
	if err != nil {
		return out, fmt.Errorf("email: ingest: modify: %w", err)
	}
	out.VersionId = string(res.VersionId)
	out.ChangeId = res.ChangeId

	// Move handler-refused records out of the outcome lists so the
	// rig's bookkeeping (e.g. its history frontier) only advances over
	// messages that actually landed.
	if len(res.Rejections) > 0 {
		rejected := make(map[string]struct{}, len(res.Rejections))
		for _, rej := range res.Rejections {
			id := rej.RecordId
			if id == "" && rej.RecordIndex >= 0 && rej.RecordIndex < len(recordIds) {
				id = recordIds[rej.RecordIndex]
			}
			if id != "" {
				rejected[id] = struct{}{}
			}
			out.Rejections = append(out.Rejections, api.OpRejection{
				RecordIndex: rej.RecordIndex,
				RecordId:    id,
				OpIndex:     rej.OpIndex,
				Reason:      rej.Reason,
			})
		}
		keep := func(ids []string) []string {
			return slices.DeleteFunc(ids, func(id string) bool {
				_, ok := rejected[id]
				return ok
			})
		}
		out.Created = keep(out.Created)
		out.Updated = keep(out.Updated)
	}
	return out, nil
}

// Patch applies the mutable allow-list fields (labelIds, historyId) to
// one existing message. The author check runs both here (clean 403 for
// local callers) and in the handler (defense-in-depth for peer
// changes); modifiedAt is bumped handler-side.
func Patch(ctx context.Context, sp space.Space, objectId, msgId, callerId string, req api.EmailPatchRequest) (space.ModifyResult, error) {
	var ops []space.Op
	if req.LabelIds != nil {
		ops = append(ops, space.Op{Type: space.OpSet, Path: FieldLabelIds, Value: *req.LabelIds})
	}
	if req.HistoryId != "" {
		ops = append(ops, space.Op{Type: space.OpSet, Path: FieldHistoryId, Value: req.HistoryId})
	}
	// Body shape before record lookup — an empty patch is a 400, not a
	// 404, regardless of whether the id exists.
	if len(ops) == 0 {
		return space.ModifyResult{}, ErrNoFields
	}
	if err := requireAuthor(ctx, sp, objectId, msgId, callerId); err != nil {
		return space.ModifyResult{}, err
	}
	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records:  []space.RecordModify{{Id: msgId, Ops: ops}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("email: patch: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("email: patch: rejected: %s", res.Rejections[0].Reason)
	}
	return res, nil
}

// Delete tombstones a message (provider-side delete/expunge). Author
// check here for the clean 403 path; the handler's BeforeDelete
// provides peer-side enforcement.
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
		return space.ModifyResult{}, fmt.Errorf("email: delete: %w", err)
	}
	return res, nil
}

// fetchExisting reads the stored records for every id in the batch in
// one $in query, keyed by id. Only the mutable fields matter to the
// caller, but the whole record comes back — the read is local.
func fetchExisting(ctx context.Context, sp space.Space, objectId string, msgs []api.EmailMessage) (map[string]*anyenc.Value, error) {
	arena := &anyenc.Arena{}
	ids := make([]*anyenc.Value, 0, len(msgs))
	for i := range msgs {
		ids = append(ids, arena.NewString(msgs[i].Id))
	}
	docs, err := sp.Query(objectId, Dataset).
		Filter(query.Key{
			Path:   []string{"id"},
			Filter: query.NewInValue(ids...),
		}).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("email: ingest: read existing: %w", err)
	}
	existing := make(map[string]*anyenc.Value, len(docs))
	for _, doc := range docs {
		existing[string(doc.GetStringBytes("id"))] = doc
	}
	return existing, nil
}

// createPayload builds the root $set object for a new message record.
// Only present fields are included — the handler validates shapes and
// derives the server-stamped ones.
func createPayload(msg *api.EmailMessage) map[string]any {
	payload := map[string]any{
		FieldThreadId:     msg.ThreadId,
		FieldInternalDate: msg.InternalDate,
	}
	if msg.From != nil {
		payload[FieldFrom] = addressValue(*msg.From)
	}
	for field, addrs := range map[string][]api.EmailAddress{
		FieldTo: msg.To, FieldCc: msg.Cc, FieldBcc: msg.Bcc, FieldReplyTo: msg.ReplyTo,
	} {
		if len(addrs) > 0 {
			payload[field] = addressValues(addrs)
		}
	}
	if msg.Subject != "" {
		payload[FieldSubject] = msg.Subject
	}
	if msg.Date != "" {
		payload[FieldDate] = msg.Date
	}
	if msg.Snippet != "" {
		payload[FieldSnippet] = msg.Snippet
	}
	if msg.BodyText != "" {
		payload[FieldBodyText] = msg.BodyText
	}
	if msg.BodyTruncated {
		payload[FieldBodyTruncated] = true
	}
	if len(msg.LabelIds) > 0 {
		payload[FieldLabelIds] = msg.LabelIds
	}
	if msg.HistoryId != "" {
		payload[FieldHistoryId] = msg.HistoryId
	}
	if len(msg.Attachments) > 0 {
		atts := make([]any, 0, len(msg.Attachments))
		for _, att := range msg.Attachments {
			entry := map[string]any{FieldAttFilename: att.Filename}
			if att.Mime != "" {
				entry[FieldAttMime] = att.Mime
			}
			if att.Size > 0 {
				entry[FieldAttSize] = att.Size
			}
			if att.FileId != "" {
				entry[FieldAttFileId] = att.FileId
			}
			atts = append(atts, entry)
		}
		payload[FieldAttachments] = atts
	}
	if msg.MessageIdHeader != "" {
		payload[FieldMessageIdHeader] = msg.MessageIdHeader
	}
	if msg.InReplyTo != "" {
		payload[FieldInReplyTo] = msg.InReplyTo
	}
	if len(msg.References) > 0 {
		payload[FieldReferences] = msg.References
	}
	return payload
}

// mutableDiffOps compares the incoming message's mutable fields
// against the stored record and returns the $set ops for the ones
// that differ. Empty means the record is already up to date.
func mutableDiffOps(stored *anyenc.Value, msg *api.EmailMessage) []space.Op {
	var ops []space.Op
	if !slices.Equal(storedStrings(stored, FieldLabelIds), msg.LabelIds) {
		labels := msg.LabelIds
		if labels == nil {
			labels = []string{}
		}
		ops = append(ops, space.Op{Type: space.OpSet, Path: FieldLabelIds, Value: labels})
	}
	if msg.HistoryId != "" && msg.HistoryId != string(stored.GetStringBytes(FieldHistoryId)) {
		ops = append(ops, space.Op{Type: space.OpSet, Path: FieldHistoryId, Value: msg.HistoryId})
	}
	return ops
}

func storedStrings(rec *anyenc.Value, field string) []string {
	items := rec.GetArray(field)
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, string(item.GetStringBytes()))
	}
	return out
}

func addressValue(a api.EmailAddress) map[string]any {
	entry := map[string]any{FieldAddrAddress: a.Address}
	if a.Name != "" {
		entry[FieldAddrName] = a.Name
	}
	return entry
}

func addressValues(addrs []api.EmailAddress) []any {
	out := make([]any, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, addressValue(a))
	}
	return out
}

// requireAuthor reads the message's creator and returns ErrNotAuthor
// when it doesn't match callerId (ErrNotFound when the message is
// absent) — same contract as chat.requireAuthor.
func requireAuthor(ctx context.Context, sp space.Space, objectId, msgId, callerId string) error {
	doc, err := sp.Query(objectId, Dataset).
		Filter(query.Key{
			Path:   []string{"id"},
			Filter: query.NewComp(query.CompOpEq, msgId),
		}).
		One(ctx)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("email: get: %w", err)
	}
	creator := doc.Get(FieldCreator)
	if creator == nil || creator.Type() != anyenc.TypeString || string(creator.GetStringBytes()) != callerId {
		return ErrNotAuthor
	}
	return nil
}
