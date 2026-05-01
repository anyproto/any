package api

// ChatMessage is the wire shape of one chat message. Mirrors the
// `chat_messages` dataset record 1:1 except for `reactions`, which is
// transposed from the storage layout (identity → emojis) into the
// emoji → identities shape clients expect. The transpose lives at the
// API layer; storage stays identity-keyed so authorization is a
// single string compare on the path (see internal/chat).
type ChatMessage struct {
	Id               string              `json:"id"`
	Creator          string              `json:"creator"`
	CreatedAt        int64               `json:"createdAt"`
	ModifiedAt       int64               `json:"modifiedAt,omitempty"`
	ReplyToMessageId string              `json:"replyToMessageId,omitempty"`
	Text             string              `json:"text"`
	Reactions        map[string][]string `json:"reactions,omitempty"`
}

// ChatSendRequest is the body of POST /v1/spaces/:spaceId/objects/:objectId/messages.
// `text` is a markdown-formatted string; rendering is the client's
// problem. Server stamps creator, createdAt, modifiedAt, chatOrder.
type ChatSendRequest struct {
	Text             string `json:"text"`
	ReplyToMessageId string `json:"replyToMessageId,omitempty"`
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
	ErrChatEmojiInvalid      = "chat.emoji_invalid"
	ErrChatUnknownField      = "chat.unknown_field"
	ErrChatNotAuthor         = "chat.not_author"
	ErrChatNotFound          = "chat.not_found"
	ErrChatRejected          = "chat.rejected"
)
