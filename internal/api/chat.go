package api

// ChatMessage is the wire shape of one chat message. Mirrors the
// `chat_messages` dataset record 1:1 except for `reactions`, which is
// rolled up from the storage layout (emoji → {accountId: timestamp})
// to the emoji → [accountId, ...] shape clients render — sorted by
// timestamp ascending. The roll-up lives at the API layer; storage
// keeps the per-identity leaf so authorization is a single
// path-segment compare (see internal/chat).
type ChatMessage struct {
	Id               string              `json:"id"`
	Creator          string              `json:"creator"`
	CreatedAt        int64               `json:"createdAt"`
	ModifiedAt       int64               `json:"modifiedAt,omitempty"`
	ReplyToMessageId string              `json:"replyToMessageId,omitempty"`
	FromAgent        string              `json:"fromAgent,omitempty"`
	Text             string              `json:"text"`
	Reactions        map[string][]string `json:"reactions,omitempty"`
}

// ChatSendRequest is the body of POST /v1/spaces/:spaceId/objects/:objectId/messages.
// `text` is a markdown-formatted string; rendering is the client's
// problem. Server stamps creator, createdAt, modifiedAt, chatOrder.
//
// `fromAgent` is an optional opaque identity string the client sets
// to mark the message as written by an agent acting on behalf of the
// signer (vs typed by the signer directly). The server does not
// validate it against any identity / signature — it's a UI hint.
type ChatSendRequest struct {
	Text             string `json:"text"`
	ReplyToMessageId string `json:"replyToMessageId,omitempty"`
	FromAgent        string `json:"fromAgent,omitempty"`
}

// ChatEditRequest is the body of PATCH .../messages/:msgId. Only
// `text` is editable post-creation — replyToMessageId is intentionally
// stable so thread structure doesn't shift. Edit by non-author returns
// 403 chat.not_author.
type ChatEditRequest struct {
	Text string `json:"text"`
}

// ChatListResponse is the body of GET .../messages. Messages are
// returned in ascending chatOrder (oldest first). Pagination cursors
// are message ids — the server resolves them to the underlying
// chatOrder boundary.
type ChatListResponse struct {
	Messages []ChatMessage `json:"messages"`
}

// ChatReactionsResponse is the body of POST
// .../messages/:msgId/reactions/:emoji. Carries the post-toggle
// reactions map for the message, transposed for the wire.
type ChatReactionsResponse struct {
	Reactions map[string][]string `json:"reactions"`
}

// Error code namespace for chat endpoints.
const (
	ErrChatTextRequired      = "chat.text_required"
	ErrChatTextTooLong       = "chat.text_too_long"
	ErrChatReplyIdInvalid    = "chat.reply_id_invalid"
	ErrChatFromAgentInvalid  = "chat.from_agent_invalid"
	ErrChatEmojiInvalid      = "chat.emoji_invalid"
	ErrChatUnknownField      = "chat.unknown_field"
	ErrChatNotAuthor         = "chat.not_author"
	ErrChatNotFound          = "chat.not_found"
	ErrChatRejected          = "chat.rejected"
)
