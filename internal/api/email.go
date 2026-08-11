package api

// EmailAddress is one parsed mailbox participant: `address` is
// required, `name` is the optional display name from the header. The
// server derives the indexed `participants` array from these — clients
// never write participants directly.
type EmailAddress struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// EmailAttachment is one entry in a message's attachment manifest —
// metadata only, the bytes go through files v2 (attach to the mailbox
// object, record the returned fileId here).
type EmailAttachment struct {
	Filename string `json:"filename"`
	Mime     string `json:"mime,omitempty"`
	Size     int64  `json:"size,omitempty"`
	FileId   string `json:"fileId,omitempty"`
}

// EmailMessage is one message in an ingest batch. `id` is the provider
// message id ([A-Za-z0-9_-], ≤ 64 bytes) and doubles as the record id
// — re-ingesting the same id upserts instead of duplicating.
// `threadId` and `internalDate` (provider receipt time, unix ms — the
// sort key) are required. `bodyText` is filtered text only (raw HTML
// is deliberately not stored); set `bodyTruncated` when the rig cut it
// at the cap. `labelIds` and `historyId` are the mutable provider
// state — on an existing record they are compared and patched, all
// other fields are immutable post-create.
type EmailMessage struct {
	Id              string            `json:"id"`
	ThreadId        string            `json:"threadId"`
	From            *EmailAddress     `json:"from,omitempty"`
	To              []EmailAddress    `json:"to,omitempty"`
	Cc              []EmailAddress    `json:"cc,omitempty"`
	Bcc             []EmailAddress    `json:"bcc,omitempty"`
	ReplyTo         []EmailAddress    `json:"replyTo,omitempty"`
	Subject         string            `json:"subject,omitempty"`
	Date            string            `json:"date,omitempty"`
	InternalDate    int64             `json:"internalDate"`
	Snippet         string            `json:"snippet,omitempty"`
	BodyText        string            `json:"bodyText,omitempty"`
	BodyTruncated   bool              `json:"bodyTruncated,omitempty"`
	LabelIds        []string          `json:"labelIds,omitempty"`
	HistoryId       string            `json:"historyId,omitempty"`
	Attachments     []EmailAttachment `json:"attachments,omitempty"`
	MessageIdHeader string            `json:"messageIdHeader,omitempty"`
	InReplyTo       string            `json:"inReplyTo,omitempty"`
	References      []string          `json:"references,omitempty"`
}

// EmailIngestRequest is the body of POST .../email/messages: one sync
// page, 1..256 messages, applied as ONE ModifyBatch (one DAG change).
type EmailIngestRequest struct {
	Messages []EmailMessage `json:"messages"`
}

// EmailIngestResult reports the per-id outcome of an ingest batch.
// `created` are ids written as new records, `updated` ids whose
// mutable fields (labelIds / historyId) changed, `unchanged` ids that
// were already stored identically (no write issued). When every id is
// unchanged no change is committed and versionId/changeId are empty.
// `rejections` passes through handler-refused records (ids listed
// nowhere else).
type EmailIngestResult struct {
	VersionId  string        `json:"versionId,omitempty"`
	ChangeId   string        `json:"changeId,omitempty"`
	Created    []string      `json:"created"`
	Updated    []string      `json:"updated"`
	Unchanged  []string      `json:"unchanged"`
	Rejections []OpRejection `json:"rejections,omitempty"`
}

// EmailPatchRequest is the body of PATCH .../email/messages/:msgId —
// the incremental label-sync path (a history round that flips labels
// without resending the message). Only the mutable allow-list is
// patchable; `labelIds` present-but-empty clears every label, absent
// leaves them alone.
type EmailPatchRequest struct {
	LabelIds  *[]string `json:"labelIds,omitempty"`
	HistoryId string    `json:"historyId,omitempty"`
}

// EmailMailboxResponse is the reply of GET .../email/mailbox — the
// deterministic mailbox object id for an address (derive-on-read, the
// agent-brain pattern). Reads go through /query on this object with
// dataset=email_messages.
type EmailMailboxResponse struct {
	ObjectId string `json:"objectId"`
}

// Error code namespace for email endpoints.
const (
	ErrEmailInvalid       = "email.invalid_message"
	ErrEmailBatchTooLarge = "email.batch_too_large"
	ErrEmailNotFound      = "email.not_found"
	ErrEmailNotAuthor     = "email.not_author"
	ErrEmailAddrInvalid   = "email.invalid_address"
)
