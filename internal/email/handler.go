package email

import (
	"errors"
	"fmt"
	"strings"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

// Indexes declares the any-store indexes ensured on each mailbox
// object's `email_messages` collection. internalDate is the sort key
// for every listing (provider receipt time), so it trails the filter
// field in each compound index. labelIds and participants are array
// fields → multikey (one entry per element), backing the
// {"labelIds": "INBOX"} / {"participants": "a@b"} eq-filters.
// participants is sparse: the handler stamps the field only when the
// message carries addresses.
func (messagesHandler) Indexes() []anystore.IndexInfo {
	return []anystore.IndexInfo{
		{Name: "idx_internal_date", Fields: []string{FieldInternalDate}},
		{Name: "idx_thread", Fields: []string{FieldThreadId, FieldInternalDate}},
		{Name: "idx_labels", Fields: []string{FieldLabelIds, FieldInternalDate}},
		{Name: "idx_participants", Fields: []string{FieldParticipants, FieldInternalDate}, Sparse: true},
	}
}

// BeforeCreate validates the ingest payload, then derives the
// server-stamped fields (creator, createdAt, modifiedAt, and the
// participants array — see deriveParticipants) via sink.Derive so they
// land alongside the rig's fields in one apply step.
//
// The expected creation shape is exactly one multi-field $set op
// (empty Path, object payload) on an EXPLICIT record id (the provider
// message id — the API layer enforces ValidMessageId before the batch
// is built; peer changes carrying a malformed id still pass through
// the dataset write, so the id shape is a wire contract, not a CRDT
// invariant).
//
// Required payload keys: threadId, internalDate. Optional: from, to,
// cc, bcc, replyTo, subject, date, snippet, bodyText, labelIds,
// historyId, attachments, messageIdHeader, inReplyTo, references. Any other key rejects — server-stamped fields (creator,
// createdAt, modifiedAt, participants) most of all, defending against
// spoofed authorship and forged participant entries.
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
	deriveParticipants(sink, op.Payload)
	return nil
}

// BeforeModify gates per-op updates. Allow-list — single-segment $set
// on a mutable field, author-only (the ingesting account), modifiedAt
// bumped:
//
//	labelIds   — validated string array (provider label state)
//	historyId  — non-empty string (provider change frontier)
//
// Everything else — addressing, body, timestamps, participants,
// _ver.* — is immutable: a changed message is a new message id on the
// provider side too.
func (messagesHandler) BeforeModify(ctx *handler.ChangeCtx, _ *handler.RecordChange, op *handler.Op, sink *handler.Sink) error {
	if op.Type != handler.OpSet || len(op.Path) != 1 {
		return rejectOp("field_not_modifiable: " + pathString(op.Path))
	}
	if !isAuthor(ctx) {
		return rejectOp("not_author")
	}
	var err error
	switch field := op.Path[0]; field {
	case FieldLabelIds:
		err = checkStringArray(field, op.Payload, MaxLabels, MaxLabelBytes)
	case FieldHistoryId:
		err = checkString(field, op.Payload, MaxHistoryIdBytes, false)
	default:
		return rejectOp("field_not_modifiable: " + field)
	}
	if err != nil {
		return err
	}
	bumpModifiedAt(ctx, sink)
	return nil
}

// BeforeDelete enforces "only the ingesting account can expunge".
func (messagesHandler) BeforeDelete(ctx *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Sink) error {
	if !isAuthor(ctx) {
		return rejectRecord("not_author")
	}
	return nil
}

// --- create validation ------------------------------------------------------

func validateCreatePayload(payload *anyenc.Value) error {
	obj, err := payload.Object()
	if err != nil {
		return rejectCreate("payload must be a JSON object")
	}
	var (
		visitErr                     error
		hasThreadId, hasInternalDate bool
	)
	obj.Visit(func(rawKey []byte, v *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldThreadId:
			hasThreadId = true
			visitErr = checkString(key, v, MaxThreadIdBytes, false)
		case FieldInternalDate:
			hasInternalDate = true
			visitErr = checkPositiveNumber(key, v)
		case FieldFrom:
			visitErr = validateAddress(key, v)
		case FieldTo, FieldCc, FieldBcc, FieldReplyTo:
			visitErr = validateAddressArray(key, v)
		case FieldSubject:
			visitErr = checkString(key, v, MaxSubjectBytes, true)
		case FieldDate:
			visitErr = checkString(key, v, MaxDateBytes, true)
		case FieldSnippet:
			visitErr = checkString(key, v, MaxSnippetBytes, true)
		case FieldBodyText:
			visitErr = checkString(key, v, MaxBodyTextBytes, true)
		case FieldLabelIds:
			visitErr = checkStringArray(key, v, MaxLabels, MaxLabelBytes)
		case FieldHistoryId:
			visitErr = checkString(key, v, MaxHistoryIdBytes, false)
		case FieldAttachments:
			visitErr = validateAttachments(v)
		case FieldMessageIdHeader, FieldInReplyTo:
			visitErr = checkString(key, v, MaxHeaderIdBytes, false)
		case FieldReferences:
			visitErr = checkStringArray(key, v, MaxReferences, MaxHeaderIdBytes)
		default:
			// Covers server-stamped fields (creator, createdAt,
			// modifiedAt, participants) and anything unknown.
			visitErr = rejectCreate("field_not_allowed: " + key)
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasThreadId {
		return rejectCreate("threadId required")
	}
	if !hasInternalDate {
		return rejectCreate("internalDate required")
	}
	return nil
}

// validateAddress gates one {name?, address} object: address required
// (non-empty, ≤ MaxAddrBytes), name optional; unknown sub-fields
// reject.
func validateAddress(key string, v *anyenc.Value) error {
	if v == nil || v.Type() != anyenc.TypeObject {
		return rejectCreate(key + " must be an object")
	}
	obj, err := v.Object()
	if err != nil {
		return rejectCreate(key + " must be an object")
	}
	var (
		visitErr   error
		hasAddress bool
	)
	obj.Visit(func(rawKey []byte, sub *anyenc.Value) {
		if visitErr != nil {
			return
		}
		switch subKey := string(rawKey); subKey {
		case FieldAddrAddress:
			hasAddress = true
			visitErr = checkString(key+".address", sub, MaxAddrBytes, false)
		case FieldAddrName:
			visitErr = checkString(key+".name", sub, MaxAddrNameBytes, true)
		default:
			visitErr = rejectCreate(key + ": field_not_allowed: " + subKey)
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasAddress {
		return rejectCreate(key + ".address required")
	}
	return nil
}

func validateAddressArray(key string, v *anyenc.Value) error {
	if v == nil || v.Type() != anyenc.TypeArray {
		return rejectCreate(key + " must be an array")
	}
	items, err := v.Array()
	if err != nil {
		return rejectCreate(key + " must be an array")
	}
	if len(items) > MaxRecipients {
		return rejectCreate(fmt.Sprintf("%s: too many entries (%d > %d)", key, len(items), MaxRecipients))
	}
	for i, item := range items {
		if err := validateAddress(fmt.Sprintf("%s[%d]", key, i), item); err != nil {
			return err
		}
	}
	return nil
}

// validateAttachments gates the attachment manifest: an array of
// {filename, mime, size, fileId?} — filename required, the rest
// optional; unknown sub-fields reject. Bytes live in files v2; this
// is metadata only.
func validateAttachments(v *anyenc.Value) error {
	if v == nil || v.Type() != anyenc.TypeArray {
		return rejectCreate("attachments must be an array")
	}
	items, err := v.Array()
	if err != nil {
		return rejectCreate("attachments must be an array")
	}
	if len(items) > MaxAttachments {
		return rejectCreate(fmt.Sprintf("attachments: too many entries (%d > %d)", len(items), MaxAttachments))
	}
	for i, item := range items {
		if item == nil || item.Type() != anyenc.TypeObject {
			return rejectCreate(fmt.Sprintf("attachments[%d] must be an object", i))
		}
		obj, err := item.Object()
		if err != nil {
			return rejectCreate(fmt.Sprintf("attachments[%d] must be an object", i))
		}
		var (
			visitErr    error
			hasFilename bool
		)
		obj.Visit(func(rawKey []byte, sub *anyenc.Value) {
			if visitErr != nil {
				return
			}
			prefix := fmt.Sprintf("attachments[%d].", i)
			switch key := string(rawKey); key {
			case FieldAttFilename:
				hasFilename = true
				visitErr = checkString(prefix+key, sub, MaxAttFieldBytes, false)
			case FieldAttMime, FieldAttFileId:
				visitErr = checkString(prefix+key, sub, MaxAttFieldBytes, false)
			case FieldAttSize:
				visitErr = checkNonNegNumber(prefix+key, sub)
			default:
				visitErr = rejectCreate(prefix + "field_not_allowed: " + key)
			}
		})
		if visitErr != nil {
			return visitErr
		}
		if !hasFilename {
			return rejectCreate(fmt.Sprintf("attachments[%d].filename required", i))
		}
	}
	return nil
}

// --- field checks -----------------------------------------------------------

func checkString(key string, v *anyenc.Value, maxBytes int, allowEmpty bool) error {
	if v == nil || v.Type() != anyenc.TypeString {
		return rejectCreate(key + " must be a string")
	}
	s := v.GetStringBytes()
	if len(s) == 0 && !allowEmpty {
		return rejectCreate(key + " must be non-empty")
	}
	if len(s) > maxBytes {
		return rejectCreate(fmt.Sprintf("%s too long (%d > %d bytes)", key, len(s), maxBytes))
	}
	return nil
}

func checkStringArray(key string, v *anyenc.Value, maxItems, maxItemBytes int) error {
	if v == nil || v.Type() != anyenc.TypeArray {
		return rejectCreate(key + " must be an array of strings")
	}
	items, err := v.Array()
	if err != nil {
		return rejectCreate(key + " must be an array of strings")
	}
	if len(items) > maxItems {
		return rejectCreate(fmt.Sprintf("%s: too many entries (%d > %d)", key, len(items), maxItems))
	}
	for i, item := range items {
		if item.Type() != anyenc.TypeString {
			return rejectCreate(fmt.Sprintf("%s[%d] must be a string", key, i))
		}
		if len(item.GetStringBytes()) > maxItemBytes {
			return rejectCreate(fmt.Sprintf("%s[%d] too long (> %d bytes)", key, i, maxItemBytes))
		}
	}
	return nil
}

func checkPositiveNumber(key string, v *anyenc.Value) error {
	if v == nil || v.Type() != anyenc.TypeNumber {
		return rejectCreate(key + " must be a number")
	}
	f, err := v.Float64()
	if err != nil || f <= 0 {
		return rejectCreate(key + " must be > 0")
	}
	return nil
}

func checkNonNegNumber(key string, v *anyenc.Value) error {
	if v == nil || v.Type() != anyenc.TypeNumber {
		return rejectCreate(key + " must be a number")
	}
	f, err := v.Float64()
	if err != nil || f < 0 {
		return rejectCreate(key + " must be ≥ 0")
	}
	return nil
}

// --- stamping & derivation --------------------------------------------------

// stampCreate queues sink.Derive ops for the row-root server-stamped
// fields. createdAt is INGEST time (the change clock), distinct from
// the provider's internalDate.
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

func bumpModifiedAt(ctx *handler.ChangeCtx, sink *handler.Sink) {
	if ctx == nil || ctx.Change == nil || sink == nil || ctx.Change.Timestamp <= 0 {
		return
	}
	a := &anyenc.Arena{}
	sink.Derive(handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldModifiedAt},
		Payload: a.NewNumberInt(int(ctx.Change.Timestamp)),
	})
}

// deriveParticipants computes and stamps the derived `participants`
// array: the normalized addresses from `from` + `to` + `cc`, deduped
// in first-occurrence order (from first). bcc is deliberately
// excluded — a bcc recipient is not visible on the message to other
// readers of the mailbox, and the rig ingesting its own sent mail can
// filter on labelIds instead. A message with no addresses carries no
// field, keeping the sparse idx_participants proportional.
//
// Server-derived (client payload rejected in validateCreatePayload):
// the addresses are already in the record, but a rig-supplied
// participants array could disagree with them — the derived stamp
// keeps the index trustworthy for every reader.
func deriveParticipants(sink *handler.Sink, payload *anyenc.Value) {
	if sink == nil || payload == nil {
		return
	}
	ids := participantsFromPayload(payload)
	if len(ids) == 0 {
		return
	}
	a := &anyenc.Arena{}
	arr := a.NewArray()
	for i, id := range ids {
		arr.SetArrayItem(i, a.NewString(id))
	}
	sink.Derive(handler.Op{Type: handler.OpSet, Path: []string{FieldParticipants}, Payload: arr})
}

// participantsFromPayload extracts the normalized address list from a
// validated create payload: from first, then to, then cc, deduped in
// first-occurrence order, capped at MaxParticipants.
func participantsFromPayload(payload *anyenc.Value) []string {
	var (
		seen = make(map[string]struct{})
		ids  []string
	)
	add := func(addr *anyenc.Value) {
		if addr == nil || addr.Type() != anyenc.TypeObject {
			return
		}
		norm := NormalizeAddress(string(addr.GetStringBytes(FieldAddrAddress)))
		if norm == "" || len(ids) >= MaxParticipants {
			return
		}
		if _, ok := seen[norm]; ok {
			return
		}
		seen[norm] = struct{}{}
		ids = append(ids, norm)
	}
	add(payload.Get(FieldFrom))
	for _, field := range []string{FieldTo, FieldCc} {
		for _, item := range payload.GetArray(field) {
			add(item)
		}
	}
	return ids
}

// isAuthor compares the record's creator with the change signer —
// fail-closed, same contract as chat.isAuthor.
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
	return strings.Join(path, ".")
}

func rejectCreate(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("email.create: %s", msg))
}

func rejectRecord(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("email: %s", msg))
}

func rejectOp(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("email: %s", msg))
}
