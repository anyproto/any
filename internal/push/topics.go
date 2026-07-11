// Desired-topic-set computation — the pure reconcile half of the
// subscription sync loop. v1 is space-level only: one notifyMode per
// space (settings.notifyMode on the tech-space row), no per-chat
// overrides yet (M3). Topic vocabulary interops with anytype-heart
// (task-push-notifications.md § Topic vocabulary).

package push

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"

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

// desiredSubs computes the account's full desired subscription state
// from a space-list snapshot: the SubscribeAll payload (one
// PushSpaceTopics per active, non-muted space) and the ids to
// RegisterSpace first (own spaces + 1-1s — heart registers when
// isOwner || isOneToOne; registration is mode-independent, since the
// registered key serves every member's subscriptions).
//
// Mode mapping (space-level v1): all → ["chats", identity]; mentions →
// [identity]; none → no topics. Only StatusActive rows participate —
// pending/declined/deleted/joining rows have no loadable ACL to derive
// push keys from.
//
// Both slices come back sorted so the result is deterministic for
// desiredHash regardless of List order.
func desiredSubs(infos []space.SpaceInfo, identity string) (subs []space.PushSpaceTopics, register []string) {
	for _, info := range infos {
		if info.Status != space.StatusActive {
			continue
		}
		if info.OwnRole == space.PermissionOwner || info.SpaceType == space.SpaceTypeOneToOne {
			register = append(register, info.Id)
		}
		switch notifyMode(info.Settings) {
		case ModeAll:
			subs = append(subs, space.PushSpaceTopics{SpaceId: info.Id, Topics: []string{TopicChats, identity}})
		case ModeMentions:
			subs = append(subs, space.PushSpaceTopics{SpaceId: info.Id, Topics: []string{identity}})
		case ModeNone:
			// muted — no topics for this space
		}
	}
	sort.Strings(register)
	sort.Slice(subs, func(i, j int) bool { return subs[i].SpaceId < subs[j].SpaceId })
	return subs, register
}

// notifyMode reads the space-level mode off the row settings. Absent,
// non-string or out-of-vocabulary values fall back to ModeAll (heart's
// default, enum 0, also its relation-removed fallback).
func notifyMode(settings map[string]any) string {
	v, _ := settings[SettingNotifyMode].(string)
	switch v {
	case ModeAll, ModeMentions, ModeNone:
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
