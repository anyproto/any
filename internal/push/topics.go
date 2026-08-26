// Desired-topic-set computation — the pure reconcile half of the
// subscription sync loop. Two granularities per space, mirroring
// anytype-heart's subscribe-side semantics (its
// core/pushnotification/topics.go bulk-vs-custom-ids branch), adapted
// to any's per-chat model: while no chat in a space carries a
// notifyMode override the space subscribes BULK topics from its
// space-level mode; as soon as any chat overrides, the space flips to
// PER-CHAT topics for every chat (effective mode = chat override ??
// space mode). Topic vocabulary interops with anytype-heart
// (docs/20-push.md § Topic vocabulary).

package push

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"slices"
	"strings"

	"github.com/anyproto/any-sync-sdk/space"
)

// SettingNotifyMode is the per-space settings key the sync loop reads
// (SpaceInfo.Settings["notifyMode"], written via
// PATCH /v1/spaces/:spaceId/settings).
const SettingNotifyMode = "notifyMode"

// notifyMode values. Heart parity: All is the default (also the
// fallback for an absent or invalid value).
const (
	ModeAll      = "all"
	ModeMentions = "mentions"
	ModeNone     = "none"
)

// TopicChats is the space-wide "all messages" topic (heart's chats
// topic). The account identity doubles as the bulk mentions topic.
const TopicChats = "chats"

// chatNotify is one chat object's contribution to a space's desired
// topic set: its object id plus the RAW `chat.notifyMode` value read
// off its objects row ("" when absent). Garbage / absent modes are
// inherit, not overrides — only a valid vocabulary value flips the
// space to per-chat topics.
type chatNotify struct {
	objectId string
	mode     string
}

// sha256hex is heart's chat-id hashing (its pushGroupId equivalent):
// per-chat topic segments and the notification groupId are the sha256
// hex of the chat object id — byte-identical for interop.
func sha256hex(id string) string {
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:])
}

// topicChat is the per-chat "all messages" topic:
// chats/<sha256hex(chatObjectId)>.
func topicChat(chatObjectId string) string {
	return TopicChats + "/" + sha256hex(chatObjectId)
}

// topicChatMention is the per-chat mention topic:
// chats/<sha256hex(chatObjectId)>/<identity>.
func topicChatMention(chatObjectId, identity string) string {
	return topicChat(chatObjectId) + "/" + identity
}

// desiredSubs computes the account's full desired subscription state
// from a space-list snapshot plus the per-space chat enumeration: the
// SubscribeAll payload (one PushSpaceTopics per active space with any
// topics) and the ids to RegisterSpace first (own spaces + 1-1s —
// heart registers when isOwner || isOneToOne; registration is
// mode-independent, since the registered key serves every member's
// subscriptions).
//
// chats maps spaceId → that space's chat objects (nil / missing key =
// no chats → space-level bulk topics, the pre-M3 behavior; a space
// whose enumeration FAILED never reaches here with a missing key —
// collectChatModes substitutes its last-known-good entries or the
// caller drops the space from infos entirely, because bulk would
// silently unmute muted chats). Only StatusActive rows participate —
// pending/declined/deleted/joining rows have no loadable ACL to derive
// push keys from.
//
// Both slices come back sorted so the result is deterministic for
// desiredHash regardless of List order (per-chat topic order is the
// caller's entry order — collectChatModes sorts by object id).
func desiredSubs(infos []space.SpaceInfo, chats map[string][]chatNotify, identity string) (subs []space.PushSpaceTopics, register []string) {
	for _, info := range infos {
		if info.Status != space.StatusActive {
			continue
		}
		if info.OwnRole == space.PermissionOwner || info.SpaceType == space.SpaceTypeOneToOne {
			register = append(register, info.Id)
		}
		if topics := spaceTopics(notifyMode(info.Settings), chats[info.Id], identity); len(topics) > 0 {
			subs = append(subs, space.PushSpaceTopics{SpaceId: info.Id, Topics: topics})
		}
	}
	slices.Sort(register)
	slices.SortFunc(subs, func(a, b space.PushSpaceTopics) int { return strings.Compare(a.SpaceId, b.SpaceId) })
	return subs, register
}

// spaceTopics maps one space onto its desired topic list — the heart
// bulk-vs-custom-ids branch adapted to per-chat overrides:
//
//   - No chat carries a valid override → BULK topics from the space
//     mode: all → ["chats", identity]; mentions → [identity]; none →
//     nothing. (The sender publishes the superset — space-wide
//     "chats", per-chat, per-chat-mention AND bare identity — so bulk
//     subscribers receive without per-chat bookkeeping.)
//   - Any chat overrides → PER-CHAT topics for EVERY chat (bulk and
//     per-chat topics don't mix — "chats" would override a muted
//     chat). Effective mode = chat.notifyMode ?? spaceMode:
//     all → chats/<sha256hex(id)> AND chats/<sha256hex(id)>/<identity>
//     (mention-adding EDITS publish only the mention topics — no room
//     re-notify — so an "all" chat must hold its own mention topic
//     too, or a newly-mentioned "all" subscriber would receive
//     nothing while a "mentions" one is pinged; multi-topic matches
//     of one message are the norm — the push server dedups per
//     device); mentions → chats/<sha256hex(id)>/<identity>; none →
//     skip the chat. The bare identity topic is deliberately absent
//     here — it belongs to the bulk branch (heart parity).
func spaceTopics(spaceMode string, entries []chatNotify, identity string) []string {
	hasOverride := false
	for _, ce := range entries {
		if validMode(ce.mode) {
			hasOverride = true
			break
		}
	}
	if !hasOverride {
		switch spaceMode {
		case ModeAll:
			return []string{TopicChats, identity}
		case ModeMentions:
			return []string{identity}
		}
		return nil // muted space
	}
	var topics []string
	for _, ce := range entries {
		eff := ce.mode
		if !validMode(eff) {
			eff = spaceMode
		}
		switch eff {
		case ModeAll:
			topics = append(topics, topicChat(ce.objectId), topicChatMention(ce.objectId, identity))
		case ModeMentions:
			topics = append(topics, topicChatMention(ce.objectId, identity))
			// ModeNone: muted chat — no topic.
		}
	}
	return topics
}

// validMode reports whether v is in the notifyMode vocabulary. The
// property is not enum-enforced at write time, so this is where
// garbage values collapse to "inherit".
func validMode(v string) bool {
	return v == ModeAll || v == ModeMentions || v == ModeNone
}

// notifyMode reads the space-level mode off the row settings. Absent,
// non-string or out-of-vocabulary values fall back to ModeAll (heart's
// default, enum 0, also its relation-removed fallback).
func notifyMode(settings map[string]any) string {
	v, _ := settings[SettingNotifyMode].(string)
	if validMode(v) {
		return v
	}
	return ModeAll
}

// desiredHash fingerprints one desired state (register set + full
// topic set) so the sync loop can skip the SubscribeAll round when
// nothing changed since the last SUCCESSFUL sync. Local compare only —
// the server's Subscriptions() returns unsigned topics keyed by
// spaceKey, not spaceId, so round-tripping it for a diff is not an
// option. Inputs must be in the deterministic order desiredSubs
// returns. Never returns "" (the "never synced" sentinel).
func desiredHash(register []string, subs []space.PushSpaceTopics) string {
	h := sha256.New()
	for _, id := range register {
		_, _ = io.WriteString(h, "r\x00"+id+"\x00")
	}
	for _, sub := range subs {
		_, _ = io.WriteString(h, "s\x00"+sub.SpaceId+"\x00")
		for _, t := range sub.Topics {
			_, _ = io.WriteString(h, t+"\x00")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
