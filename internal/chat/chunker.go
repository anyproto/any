package chat

import (
	"context"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/index"
)

// NewChunker constructs the chat module chunker: chat_messages records
// as index entries under scope "chat", one entry per message, on every
// chat collection the space declares (the canonical one, chat being
// shared-only). Data is the message `text` only — creator, reactions,
// and attachments are deliberately excluded. Deleted messages (and
// empty-text messages) yield Data "".
func NewChunker() *index.ModuleChunker {
	return index.NewModuleChunker(Module, index.ModuleStream(streamMessages))
}

// streamMessages streams one chat collection's messages past the
// cursor, ascending by ApplySeq. Deleted rows yield a tombstone (Data
// ""); live rows yield their text.
func streamMessages(ctx context.Context, sp space.Space, objectId, collection string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, collection)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeChat,
			ObjectId: objectId,
			Dataset:  collection,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = messageData(rec)
			entry.Links = MessageLinks(sp.Id(), objectId, collection, entry.RecordId, rec)
		}
		return yield(entry)
	})
}

// MessageLinks extracts one message's edges: every any:// reference in
// its text (mentions as mention, the rest as link), each attachment's
// `link` and the agent group's `debugLink` (kind link; a non-any://
// value — an http attachment — is skipped).
func MessageLinks(spaceId, objectId, collection, msgId string, rec *anyenc.Value) []index.LinkEntry {
	if rec == nil {
		return nil
	}
	out := index.TextLinks(spaceId, objectId, collection, msgId, string(rec.GetStringBytes(FieldText)))
	var refs []string
	if att := rec.Get(FieldAttachments); att != nil && att.Type() == anyenc.TypeObject {
		obj, _ := att.Object()
		obj.Visit(func(_ []byte, a *anyenc.Value) {
			if l := a.GetStringBytes(FieldAttachmentLink); len(l) > 0 {
				refs = append(refs, string(l))
			}
		})
	}
	if l := rec.GetStringBytes(FieldAgent, FieldAgentDebugLink); len(l) > 0 {
		refs = append(refs, string(l))
	}
	if len(refs) == 0 {
		return out
	}
	seen := make(map[string]bool, len(out))
	for _, e := range out {
		seen[e.Target.String()] = true
	}
	for _, e := range index.ValueLinks(spaceId, objectId, collection, msgId, index.LinkModeMany, stringArrayValue(refs)) {
		key := e.Target.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		e.Kind = index.LinkKindLink
		if e.Target.Kind == anyuri.KindMention {
			e.Kind = index.LinkKindMention
		}
		out = append(out, e)
	}
	return out
}

// stringArrayValue builds an anyenc string array on a fresh arena.
func stringArrayValue(vals []string) *anyenc.Value {
	a := &anyenc.Arena{}
	arr := a.NewArray()
	for i, s := range vals {
		arr.SetArrayItem(i, a.NewString(s))
	}
	return arr
}

// messageData extracts the indexable text of a message — the `text`
// field only. Empty when absent.
func messageData(rec *anyenc.Value) string {
	if rec == nil {
		return ""
	}
	return string(rec.GetStringBytes(FieldText))
}
