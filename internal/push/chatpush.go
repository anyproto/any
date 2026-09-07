// Chat notify hooks — the sender side of push. The HTTP
// chat handlers call these after a successful send / edit / read;
// each builds heart's wire payload + topic superset and feeds
// Enqueue. Sender-scoped BY CONSTRUCTION: hooks fire only on this
// server's own writes (a remote peer's message was pushed by ITS
// sender), which is exactly why this is a handler hook and not a
// Changes() feed — see docs/20-push.md § Triggers.
//
// Everything here is best-effort and MUST NOT block or fail the HTTP
// response: the record read-back runs on a service-lifetime goroutine
// and read errors log-and-skip (the message itself is already synced;
// push is a side channel).

package push

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/chat"
)

// Wire payload — heart's core/block/chats/chatpush/push.go verbatim
// (JSON field names MUST stay byte-identical: the receiving mobile
// extensions decode this exact shape). Pinned by
// TestChatPayload_HeartWireFormat.

// Type discriminates payload kinds. Heart parity.
type Type int

// ChatMessage is the only payload type v1 emits (message create +
// mention-adding edits; reads go through the silent path with no
// payload).
const ChatMessage Type = 1

// Payload is the outer (pre-encryption) notification body.
type Payload struct {
	SpaceId string `json:"spaceId,omitempty"`
	// SpaceUxType / SpaceType are heart's model.SpaceUxType /
	// model.SpaceType enums, filled by heartSpaceKinds from the space's
	// type: the payload is the only thing a receiver that has never
	// seen the space can classify on (a 1-1 renders as a direct
	// message, not a channel). Unmapped space types stay 0 (unknown).
	SpaceUxType       int                `json:"spaceUxType"`
	SpaceType         int                `json:"spaceType"`
	SenderId          string             `json:"senderId"`
	Type              Type               `json:"type"`
	NewMessagePayload *NewMessagePayload `json:"newMessage,omitempty"`
}

// NewMessagePayload carries the renderable message preview.
type NewMessagePayload struct {
	ChatId         string        `json:"chatId"`
	MsgId          string        `json:"msgId"`
	SpaceName      string        `json:"spaceName"`
	ChatName       string        `json:"chatName"`
	SenderName     string        `json:"senderName"`
	Text           string        `json:"text"`
	HasAttachments bool          `json:"hasAttachments"`
	Attachments    []*Attachment `json:"attachments"`
}

// Attachment mirrors heart's per-attachment stub. Layout is heart's
// resolvedLayout from object details; any's chat attachments are
// {type, link} hints with no layout notion, so it stays 0.
type Attachment struct {
	Layout int `json:"layout"`
}

// maxPushTextRunes caps the payload text (heart truncates the same
// way — the push preview never needs the full message).
const maxPushTextRunes = 1024

// hookBudget bounds one hook's read-back + payload build. The reads
// are local (any-store), so this is generous; it exists so a wedged
// store can't pin hook goroutines past shutdown attempts.
const hookBudget = 30 * time.Second

// NotifyChatMessage is the chatSend hook: read the just-written
// message back (for the server-derived `mentions` + text), then
// enqueue a loud notification on the full topic superset —
//
//	chats                                   space-wide "all"
//	chats/<sha256hex(chatId)>               per-chat "all"
//	chats/<sha256hex(chatId)>/<mention>     per-chat mention
//	<mention>                               bare identity (bulk mentions)
//
// groupId = sha256hex(chatId). Asynchronous and non-blocking — safe
// to call on the handler goroutine after chat.Send succeeds.
func (s *Service) NotifyChatMessage(sp space.Space, objectId, msgId string) {
	s.goHook(func(ctx context.Context) {
		rec, err := chatRecord(ctx, sp, objectId, msgId)
		if err != nil {
			s.lg.Warn("push chat send hook: message read-back failed — push skipped",
				zap.String("spaceId", sp.Id()), zap.Error(err))
			return
		}
		mentions := stringArray(rec, chat.FieldMentions)
		topics := make([]string, 0, 2+2*len(mentions))
		topics = append(topics, TopicChats, topicChat(objectId))
		for _, m := range mentions {
			topics = append(topics, topicChatMention(objectId, m), m)
		}
		payload, err := s.chatPayload(ctx, sp, objectId, msgId, rec)
		if err != nil {
			s.lg.Warn("push chat send hook: payload build failed — push skipped",
				zap.String("spaceId", sp.Id()), zap.Error(err))
			return
		}
		s.Enqueue(sp.Id(), topics, payload, sha256hex(objectId), false)
	})
}

// ChatMentionsBefore snapshots a message's server-derived mentions —
// the chatEdit hook's "before" set. SYNCHRONOUS by necessity (it must
// read the pre-edit record, so the handler calls it before the edit
// lands); one local indexed read, so the handler cost is negligible.
// ok=false means the read failed and the edit push should be skipped
// entirely — without a before set the diff would over-notify every
// existing mention on every edit.
func (s *Service) ChatMentionsBefore(ctx context.Context, sp space.Space, objectId, msgId string) (mentions []string, ok bool) {
	rec, err := chatRecord(ctx, sp, objectId, msgId)
	if err != nil {
		s.lg.Debug("push chat edit hook: pre-edit read failed — edit push skipped",
			zap.String("spaceId", sp.Id()), zap.Error(err))
		return nil, false
	}
	return stringArray(rec, chat.FieldMentions), true
}

// NotifyChatEdit is the chatEdit hook: re-read the message, diff its
// mentions against the pre-edit snapshot, and push only to the
// NEWLY-ADDED mentions — bare identity + per-chat mention topics
// only, never the "chats" / per-chat broadcast topics (an edit is not
// a new message for the room; only a fresh ping notifies, decision 1
// in docs/20-push.md § Triggers). Asynchronous and non-blocking.
func (s *Service) NotifyChatEdit(sp space.Space, objectId, msgId string, before []string) {
	s.goHook(func(ctx context.Context) {
		rec, err := chatRecord(ctx, sp, objectId, msgId)
		if err != nil {
			s.lg.Warn("push chat edit hook: message read-back failed — push skipped",
				zap.String("spaceId", sp.Id()), zap.Error(err))
			return
		}
		added := addedMentions(before, stringArray(rec, chat.FieldMentions))
		if len(added) == 0 {
			return
		}
		topics := make([]string, 0, 2*len(added))
		for _, m := range added {
			topics = append(topics, topicChatMention(objectId, m), m)
		}
		payload, err := s.chatPayload(ctx, sp, objectId, msgId, rec)
		if err != nil {
			s.lg.Warn("push chat edit hook: payload build failed — push skipped",
				zap.String("spaceId", sp.Id()), zap.Error(err))
			return
		}
		s.Enqueue(sp.Id(), topics, payload, sha256hex(objectId), false)
	})
}

// NotifyChatRead is the chatRead / chatReadAll hook: a SILENT
// notification with the chat's groupId (topics nil — the server
// targets the caller's own-identity topic on the silent path), so the
// account's other devices wake and refresh their badges. Heart hooks
// its read RPCs the same way. Non-blocking (plain Enqueue).
func (s *Service) NotifyChatRead(spaceId, objectId string) {
	s.Enqueue(spaceId, nil, nil, sha256hex(objectId), true)
}

// --- internals -------------------------------------------------------

// goHook runs fn on a tracked goroutine bounded by the service
// lifetime + hookBudget. No-op before Start or after Close — hooks
// are best-effort, and a server tearing down has no business pushing.
func (s *Service) goHook(fn func(ctx context.Context)) {
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, hookBudget)
		defer cancel()
		fn(ctx)
	}()
}

// chatRecord reads one chat_messages record by id — the same
// per-object query shape internal/chat's own read-backs use.
func chatRecord(ctx context.Context, sp space.Space, objectId, msgId string) (*anyenc.Value, error) {
	return sp.Query(objectId, chat.Dataset).
		Filter(query.Key{Path: []string{"id"}, Filter: query.NewComp(query.CompOpEq, msgId)}).
		One(ctx)
}

// heart's enum values `any` maps onto — only the pair the receiver
// branches on (heart's model.SpaceUxType_OneToOne /
// model.SpaceType_SpaceTypeOneToOne). Every other space type stays 0
// (unknown), so a client keeps its channel rendering for those.
const (
	heartSpaceUxTypeOneToOne = 4
	heartSpaceTypeOneToOne   = 4
)

// heartSpaceKinds returns heart's (spaceUxType, spaceType) pair for an
// `any` space type.
func heartSpaceKinds(spaceType string) (uxType, kind int) {
	if spaceType == space.SpaceTypeOneToOne {
		return heartSpaceUxTypeOneToOne, heartSpaceTypeOneToOne
	}
	return 0, 0
}

// chatPayload builds the heart-wire Payload JSON for one message
// record. Enrichment is all local and best-effort: spaceName from the
// space's Info snapshot, chatName from the chat object's `any.name`
// (empty when unset), senderName from the account profile (empty when
// no profile was ever written).
func (s *Service) chatPayload(ctx context.Context, sp space.Space, objectId, msgId string, rec *anyenc.Value) ([]byte, error) {
	senderName := ""
	if meta, present, err := s.sdk.Account().Metadata(ctx); err == nil && present {
		senderName = meta.Name
	}
	chatName := ""
	if props, err := sp.Properties().Get(ctx, objectId); err == nil && props != nil {
		chatName = string(props.GetStringBytes("any", "name"))
	}
	var atts []*Attachment
	if attObj := rec.GetObject(chat.FieldAttachments); attObj != nil {
		attObj.Visit(func(_ []byte, _ *anyenc.Value) {
			atts = append(atts, &Attachment{})
		})
	}
	info := sp.Info()
	uxType, kind := heartSpaceKinds(info.SpaceType)
	return json.Marshal(Payload{
		SpaceId:     sp.Id(),
		SpaceUxType: uxType,
		SpaceType:   kind,
		SenderId:    s.sdk.Account().Id(),
		Type:        ChatMessage,
		NewMessagePayload: &NewMessagePayload{
			ChatId:         objectId,
			MsgId:          msgId,
			SpaceName:      info.Name,
			ChatName:       chatName,
			SenderName:     senderName,
			Text:           truncateRunes(string(rec.GetStringBytes(chat.FieldText)), maxPushTextRunes),
			HasAttachments: len(atts) > 0,
			Attachments:    atts,
		},
	})
}

// addedMentions returns the identities present in after but not in
// before, preserving after's order.
func addedMentions(before, after []string) []string {
	if len(after) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(before))
	for _, m := range before {
		seen[m] = struct{}{}
	}
	var added []string
	for _, m := range after {
		if _, ok := seen[m]; !ok {
			added = append(added, m)
		}
	}
	return added
}

// stringArray reads a string-array field off an anyenc record
// (non-string elements skipped; nil when absent).
func stringArray(rec *anyenc.Value, field string) []string {
	vals := rec.GetArray(field)
	if len(vals) == 0 {
		return nil
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v.Type() == anyenc.TypeString {
			out = append(out, string(v.GetStringBytes()))
		}
	}
	return out
}

// truncateRunes caps s at n runes (never splitting a rune).
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s // fewer bytes than n ⇒ fewer runes than n
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
