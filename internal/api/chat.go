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

// ChatAgentMeta marks a message as agent-authored. `Name` is the
// display label; like the old fromAgent tag it is NOT verified against
// any identity / signature — `creator` stays the change signer.
// `DebugLink` is an opaque drill-down link into the run's debug page,
// by convention `any://<spaceId>/<debugLogObjectId>[#turn_<n>]`.
// `Done` is liveness: false means the run that produced this message
// is still going (clients show a typing indicator until a done:true
// message lands). The whole group is create-only and immutable.
type ChatAgentMeta struct {
	Name      string `json:"name"`
	DebugLink string `json:"debugLink,omitempty"`
	Done      bool   `json:"done"`
}

// ChatSendRequest is the body of POST /v1/spaces/:spaceId/objects/:objectId/messages.
// `text` is a markdown-formatted string; rendering is the client's
// problem. Server stamps creator, createdAt, modifiedAt.
//
// `agent` is optional and marks the message as written by an agent
// acting on behalf of the signer (vs typed by the signer directly).
//
// `attachments` is a map keyed by short opaque ids (≤ 64 chars,
// [A-Za-z0-9_-]+) carrying {type, link, order?}. Create-only.
type ChatSendRequest struct {
	Text             string                    `json:"text"`
	ReplyToMessageId string                    `json:"replyToMessageId,omitempty"`
	Agent            *ChatAgentMeta            `json:"agent,omitempty"`
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
	ErrChatAgentInvalid       = "chat.agent_invalid"
	ErrChatEmojiInvalid       = "chat.emoji_invalid"
	ErrChatUnknownField       = "chat.unknown_field"
	ErrChatNotAuthor          = "chat.not_author"
	ErrChatNotFound           = "chat.not_found"
	ErrChatRejected           = "chat.rejected"
	ErrChatAttachmentsInvalid = "chat.attachments_invalid"
)
