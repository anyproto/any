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

// ChatMessageContext is where the sender was when they sent the
// message — the page on screen, stamped by the client. `SpaceId` is
// the space in view (required), `ObjectId` the open object /
// collection / record when there is one, `View` the client's view
// kind (an open string: "object", "collection", "mail", …). Optional
// on send, create-only and immutable; an agent reading the chat takes
// "here"/"this page" from it. No timestamp: the message's createdAt is
// when the user was there.
type ChatMessageContext struct {
	SpaceId  string `json:"spaceId"`
	ObjectId string `json:"objectId,omitempty"`
	View     string `json:"view,omitempty"`
}

// Chat writes (send / edit / delete / react) return the shared
// api.ModifyResult — {versionId, changeId, recordIds} — like every
// other dataset write. recordIds[0] is the new message id on send
// (server-derived) and the target id on edit / delete / react. The
// message record itself is read back through POST /query (or live via
// /query/subscribe) with dataset=chat_messages; there is no curated
// per-message wire struct. See docs/03-api.md § Chat.

// ChatMessageControl is a client's signal to the agent serving the
// chat, carried on a message of its own (text may be empty). `Kind` is
// an open string the agent interprets — `break` asks the run in flight
// to stop; `Hard` = now (vs. wrap up at the next turn). Create-only,
// immutable; a client renders it as a marker, not a bubble.
type ChatMessageControl struct {
	Kind string `json:"kind"`
	Hard bool   `json:"hard,omitempty"`
}

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
	// Outcome says how the run behind a done:true message ended when
	// it did not end normally — `interrupted` (the user stopped it),
	// `error` (it died). Absent on a normal reply. Opaque to the
	// server; clients key their rendering (a stop mark, a warning) on
	// it instead of parsing the text.
	Outcome string `json:"outcome,omitempty"`
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
//
// `context` is the sender's view at send time (ChatMessageContext).
// Optional, create-only.
//
// `control` is a signal to the agent (ChatMessageControl) — the one
// case where `text` may be empty. Optional, create-only.
type ChatSendRequest struct {
	Text             string                    `json:"text"`
	ReplyToMessageId string                    `json:"replyToMessageId,omitempty"`
	Agent            *ChatAgentMeta            `json:"agent,omitempty"`
	Attachments      map[string]ChatAttachment `json:"attachments,omitempty"`
	Context          *ChatMessageContext       `json:"context,omitempty"`
	Control          *ChatMessageControl       `json:"control,omitempty"`
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
	ErrChatContextInvalid     = "chat.context_invalid"
	ErrChatControlInvalid     = "chat.control_invalid"
)
