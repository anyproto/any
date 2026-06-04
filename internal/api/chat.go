package api

// ChatAttachment is one entry in a message's attachments map. `Type`
// is an open enum — known values are "link" and "image"; clients fall
// back to rendering `Link` as a plain anchor for unknown types. `Link`
// is the URL (any:// for in-space references, https:// or similar for
// out-of-space resources). Attachments are immutable post-create.
type ChatAttachment struct {
	Type string `json:"type"`
	Link string `json:"link"`
}

// Chat writes (send / edit / delete / react) return the shared
// api.ModifyResult — {versionId, changeId, recordIds} — like every
// other dataset write. recordIds[0] is the new message id on send
// (server-derived) and the target id on edit / delete / react. The
// message record itself is read back through POST /query (or live via
// /query/subscribe) with dataset=chat_messages; there is no curated
// per-message wire struct. See docs/03-api.md § Chat.

// ChatSendRequest is the body of POST /v1/spaces/:spaceId/objects/:objectId/messages.
// `text` is a markdown-formatted string; rendering is the client's
// problem. Server stamps creator, createdAt, modifiedAt.
//
// `fromAgent` is an optional opaque identity string the client sets
// to mark the message as written by an agent acting on behalf of the
// signer (vs typed by the signer directly). The server does not
// validate it against any identity / signature — it's a UI hint.
//
// `attachments` is a map keyed by short opaque ids (≤ 64 chars,
// [A-Za-z0-9_-]+) carrying {type, link, order?}. Create-only.
type ChatSendRequest struct {
	Text             string                    `json:"text"`
	ReplyToMessageId string                    `json:"replyToMessageId,omitempty"`
	FromAgent        string                    `json:"fromAgent,omitempty"`
	Attachments      map[string]ChatAttachment `json:"attachments,omitempty"`
}

// ChatEditRequest is the body of PATCH .../messages/:msgId. Only
// `text` is editable post-creation — replyToMessageId is intentionally
// stable so thread structure doesn't shift. Edit by non-author returns
// 403 chat.not_author.
type ChatEditRequest struct {
	Text string `json:"text"`
}

// Error code namespace for chat endpoints.
const (
	ErrChatTextRequired       = "chat.text_required"
	ErrChatTextTooLong        = "chat.text_too_long"
	ErrChatReplyIdInvalid     = "chat.reply_id_invalid"
	ErrChatFromAgentInvalid   = "chat.from_agent_invalid"
	ErrChatEmojiInvalid       = "chat.emoji_invalid"
	ErrChatUnknownField       = "chat.unknown_field"
	ErrChatNotAuthor          = "chat.not_author"
	ErrChatNotFound           = "chat.not_found"
	ErrChatRejected           = "chat.rejected"
	ErrChatAttachmentsInvalid = "chat.attachments_invalid"
)
