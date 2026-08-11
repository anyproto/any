// Package email defines the built-in `email` type — synced mail
// mirrored from an external provider (gmail first), stored as one
// record per message on a per-mailbox object's `email_messages`
// dataset. Replaces the object-per-email scheme sync rigs otherwise
// invent (one CRDT tree, nav row, and editor-block body per email).
//
// Model:
//
//   - One mailbox object per synced address, derived deterministically
//     from MailboxSeedPrefix + the normalized address (mailbox.go) —
//     the brain / general-chat mechanic, so every device and every
//     re-bootstrapped rig converges on the same object.
//   - Record id = the provider message id (ValidMessageId). The id is
//     the idempotency key: re-ingesting a sync page can never
//     duplicate a message.
//   - Messages are write-once except a small mutable allow-list
//     (labelIds, historyId) — provider state that changes after
//     delivery. Everything else is immutable post-create.
//   - The provider is the source of truth, so the dataset sets
//     SkipHistory (chat/agentlog precedent): label churn must not
//     accrete version-history rows.
//
// Wire / storage shape of one message record:
//
//	{
//	  "id":              "<provider message id>",
//	  "_ver":            { ... },                  // SDK-managed
//	  "creator":         "<accountId>",            // server-stamped
//	  "createdAt":       <unix-seconds>,           // server-stamped (ingest time)
//	  "modifiedAt":      <unix-seconds>,           // bumped on label patch
//	  "threadId":        "<provider thread id>",   // required
//	  "from":            {"name": "...", "address": "a@b"},
//	  "to":              [{"name": ..., "address": ...}, ...],
//	  "cc":              [...], "bcc": [...], "replyTo": [...],
//	  "subject":         "...",
//	  "date":            "<RFC header value>",     // display-only
//	  "internalDate":    <unix-ms>,                // required — THE sort key
//	  "snippet":         "...",
//	  "bodyText":        "<text-extracted body>",  // filtered text only, capped
//	  "bodyTruncated":   <bool>,                   // optional
//	  "labelIds":        ["INBOX", "UNREAD", ...], // MUTABLE
//	  "historyId":       "<provider history id>",  // MUTABLE
//	  "attachments":     [{"filename", "mime", "size", "fileId"?}, ...],
//	  "messageIdHeader": "<Message-ID>",           // RFC-822 threading
//	  "inReplyTo":       "<In-Reply-To>",
//	  "references":      ["<Message-ID>", ...],
//	  "participants":    ["a@b", ...],             // server-DERIVED
//	}
//
// `participants` is server-derived (client writes rejected — the chat
// mentions mechanic): the normalized lowercase addresses folded from
// from + to + cc, deduped in first-occurrence order. A multikey index
// backs `{"participants": "a@b"}` filters, so per-correspondent views
// need no per-email contact links. Absent when the message carries no
// addresses — the sparse index stays proportional to addressed mail.
//
// Chronological order is `internalDate` (provider receipt time), NOT
// `_ver.id` — our sync order is meaningless for mail (a backfill
// ingests newest-first, an incremental sync oldest-first).
//
// `labelIds` is a whole-array LWW field, not a per-label map: the
// multikey index makes the common filters ({"labelIds": "INBOX"})
// cheap, and one sync writer per mailbox is the documented assumption.
// Flip to the reactions-style per-leaf map only if multi-writer label
// editing becomes real.
//
// Bodies are FILTERED TEXT ONLY (bodyText, capped at MaxBodyTextBytes
// with bodyTruncated marking the cut). Raw HTML / RFC822 is
// deliberately not stored; attachment bytes go through files v2 with
// the fileId recorded in the attachments manifest.
//
// The sibling `email_sync_state` dataset on the same mailbox object
// holds sync-pipeline cursors (historyId frontier etc.) — a raw
// DefaultHandler dataset (agentmem job-state precedent): the rig owns
// the shape and writes through the generic /v1/spaces/:id/modify
// route, one record per sync source.
package email

import (
	"context"

	"github.com/anyproto/any-sync-sdk/handler"
)

// TypeId is reserved — content-addressable user type ids never
// produce this string. NOTE: sync rigs that predate the built-in
// created USER types with xKey "email"; those are content-addressed
// CID ids and do not collide with this literal.
const TypeId = "email"

// Display metadata used when surfacing the built-in via Types.List.
const (
	Name        = "Email"
	Description = "Synced mail mirror: one record per message on a per-mailbox object"
)

// Dataset is the per-object dataset holding the message records.
const Dataset = "email_messages"

// DatasetSyncState holds sync-pipeline cursors on the same mailbox
// object — raw DefaultHandler, shape owned by the sync rig, written
// through the generic /modify route (one record per sync source).
const DatasetSyncState = "email_sync_state"

// Field keys on a message record.
const (
	FieldCreator         = "creator"
	FieldCreatedAt       = "createdAt"
	FieldModifiedAt      = "modifiedAt"
	FieldThreadId        = "threadId"
	FieldFrom            = "from"
	FieldTo              = "to"
	FieldCc              = "cc"
	FieldBcc             = "bcc"
	FieldReplyTo         = "replyTo"
	FieldSubject         = "subject"
	FieldDate            = "date"
	FieldInternalDate    = "internalDate"
	FieldSnippet         = "snippet"
	FieldBodyText        = "bodyText"
	FieldBodyTruncated   = "bodyTruncated"
	FieldLabelIds        = "labelIds"
	FieldHistoryId       = "historyId"
	FieldAttachments     = "attachments"
	FieldMessageIdHeader = "messageIdHeader"
	FieldInReplyTo       = "inReplyTo"
	FieldReferences      = "references"
	FieldParticipants    = "participants"
)

// Address sub-object keys ({name?, address}).
const (
	FieldAddrName    = "name"
	FieldAddrAddress = "address"
)

// Attachment sub-object keys. `fileId` is optional — present once the
// rig has attached the bytes through files v2.
const (
	FieldAttFilename = "filename"
	FieldAttMime     = "mime"
	FieldAttSize     = "size"
	FieldAttFileId   = "fileId"
)

// dataVersion is the on-the-wire stamp pinned to writes on this
// dataset. Bump only when validation logic changes in a way that must
// reject older writers.
const dataVersion = "email_messages-v1"

// Validation limits. Conservative; revisit if real usage hits them.
const (
	MaxMessageIdBytes = 64         // record id (provider message id)
	MaxThreadIdBytes  = 256
	MaxAddrNameBytes  = 512
	MaxAddrBytes      = 320        // RFC 5321 max path
	MaxRecipients     = 256        // per to/cc/bcc/replyTo array
	MaxSubjectBytes   = 2 * 1024
	MaxDateBytes      = 256
	MaxSnippetBytes   = 2 * 1024
	MaxBodyTextBytes  = 128 * 1024 // filtered text; rigs truncate + set bodyTruncated
	MaxLabels         = 128
	MaxLabelBytes     = 256
	MaxHistoryIdBytes = 64
	MaxAttachments    = 64
	MaxAttFieldBytes  = 1024 // filename / mime / fileId
	MaxHeaderIdBytes  = 1024 // messageIdHeader / inReplyTo / references entries
	MaxReferences     = 64

	// MaxParticipants caps the derived participants array (post-dedup,
	// first-occurrence order; from wins the first slot). MaxRecipients
	// already bounds real mail far below this — the cap is a defense
	// against pathological stuffing, not a product limit.
	MaxParticipants = 256

	// MaxIngestBatch bounds one POST …/email/messages request — one
	// sync page, one ModifyBatch, one DAG change.
	MaxIngestBatch = 256
)

// NewType returns the handler.Type to add to config.Config.Types so
// the SDK accepts writes on the email datasets.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{
				Name:        Dataset,
				DataVersion: dataVersion,
				Handler:     messagesHandler{},
				Schema:      datasetSchema(),
				Indexes:     messagesHandler{}.Indexes(),
				// No version history: the provider is the source of
				// truth and label churn is constant — nothing reads a
				// per-message timeline. The DAG keeps everything;
				// flipping this later only costs a backfill.
				SkipHistory: true,
			},
			// Sync cursors: rig-owned shape, no validation, no history.
			{Name: DatasetSyncState, DataVersion: "1", Handler: handler.DefaultHandler{}, SkipHistory: true},
		},
	}
}

// datasetSchema declares the email_messages field schema for apply-time
// enforcement and discovery (Space.Datasets / GET .../datasets).
//
// Dynamic so undeclared keys stay permitted (forward-compat with rigs
// that stash provider extras), but every known field is declared with
// its class: creator / createdAt / modifiedAt / participants are
// server-stamped via sink.Derive → ScopeDerived; everything else the
// rig writes → ScopeSynced.
func datasetSchema() handler.Schema {
	return handler.Schema{
		Dynamic: true,
		Fields: []handler.Field{
			{Id: FieldCreator, Name: "Creator", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeDerived},
			{Id: FieldCreatedAt, Name: "Created At", Schema: handler.Leaf(handler.PropertyKindNumber), Scope: handler.ScopeDerived},
			{Id: FieldModifiedAt, Name: "Modified At", Schema: handler.Leaf(handler.PropertyKindNumber), Scope: handler.ScopeDerived},
			{Id: FieldParticipants, Name: "Participants", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeDerived},
			{Id: FieldThreadId, Name: "Thread ID", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldFrom, Name: "From", Schema: handler.Leaf(handler.PropertyKindObject), Scope: handler.ScopeSynced},
			{Id: FieldTo, Name: "To", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeSynced},
			{Id: FieldCc, Name: "Cc", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeSynced},
			{Id: FieldBcc, Name: "Bcc", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeSynced},
			{Id: FieldReplyTo, Name: "Reply-To", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeSynced},
			{Id: FieldSubject, Name: "Subject", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldDate, Name: "Date", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldInternalDate, Name: "Internal Date", Schema: handler.Leaf(handler.PropertyKindNumber), Scope: handler.ScopeSynced},
			{Id: FieldSnippet, Name: "Snippet", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldBodyText, Name: "Body Text", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldBodyTruncated, Name: "Body Truncated", Schema: handler.Leaf(handler.PropertyKindBoolean), Scope: handler.ScopeSynced},
			{Id: FieldLabelIds, Name: "Label IDs", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeSynced},
			{Id: FieldHistoryId, Name: "History ID", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldAttachments, Name: "Attachments", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeSynced},
			{Id: FieldMessageIdHeader, Name: "Message-ID", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldInReplyTo, Name: "In-Reply-To", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldReferences, Name: "References", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeSynced},
		},
	}
}

// messagesHandler is the CRDT handler owning the email_messages
// dataset. Implementation lives in handler.go.
type messagesHandler struct {
	handler.DefaultHandler
}

func (messagesHandler) Init(_ context.Context) error { return nil }

// ValidMessageId reports whether id is a legal email record id:
// non-empty, ≤ MaxMessageIdBytes, alphabet [A-Za-z0-9_-]. Provider
// message ids (gmail hex ids, Message-ID hashes) fit; path separators
// and dots are excluded so ids are safe as URL segments and any-store
// keys.
func ValidMessageId(id string) bool {
	if len(id) == 0 || len(id) > MaxMessageIdBytes {
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
