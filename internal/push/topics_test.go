package push

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
)

const testIdentity = "AAliceIdentity"

func activeSpace(id, mode string) space.SpaceInfo {
	info := space.SpaceInfo{
		Id:        id,
		SpaceType: space.SpaceTypeRegular,
		Status:    space.StatusActive,
		OwnRole:   space.PermissionWriter,
	}
	if mode != "" {
		info.Settings = map[string]any{SettingNotifyMode: mode}
	}
	return info
}

func TestDesiredSubs_Modes(t *testing.T) {
	cases := []struct {
		name   string
		mode   any // value stored under settings.notifyMode; nil = absent settings
		topics []string
	}{
		{"all", ModeAll, []string{TopicChats, testIdentity}},
		{"mentions", ModeMentions, []string{testIdentity}},
		{"none", ModeNone, nil},
		{"absent settings", nil, []string{TopicChats, testIdentity}},
		{"garbage string", "loud", []string{TopicChats, testIdentity}},
		{"non-string value", 42.0, []string{TopicChats, testIdentity}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := activeSpace("sp1", "")
			if tc.mode != nil {
				info.Settings = map[string]any{SettingNotifyMode: tc.mode}
			}
			subs, _ := desiredSubs([]space.SpaceInfo{info}, nil, testIdentity)
			if tc.topics == nil {
				if len(subs) != 0 {
					t.Fatalf("mode none should yield no subs, got %+v", subs)
				}
				return
			}
			if len(subs) != 1 || subs[0].SpaceId != "sp1" {
				t.Fatalf("subs = %+v", subs)
			}
			if !reflect.DeepEqual(subs[0].Topics, tc.topics) {
				t.Errorf("topics = %v, want %v", subs[0].Topics, tc.topics)
			}
		})
	}
}

// sha is the test-local mirror of sha256hex, so expectations spell out
// the construction independently.
func sha(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

// TestDesiredSubs_PerChat pins the bulk-vs-per-chat branch (heart's
// subscribe-side semantics adapted to per-chat notifyMode overrides).
func TestDesiredSubs_PerChat(t *testing.T) {
	cases := []struct {
		name      string
		spaceMode string // "" = absent settings (defaults to all)
		chats     []chatNotify
		topics    []string // nil = no PushSpaceTopics entry for the space
	}{
		{
			name:      "chats without overrides stay bulk",
			spaceMode: ModeAll,
			chats:     []chatNotify{{objectId: "c1"}, {objectId: "c2"}},
			topics:    []string{TopicChats, testIdentity},
		},
		{
			name:      "garbage-only modes stay bulk (garbage = inherit, not override)",
			spaceMode: ModeAll,
			chats:     []chatNotify{{objectId: "c1", mode: "loud"}, {objectId: "c2", mode: "ALL"}},
			topics:    []string{TopicChats, testIdentity},
		},
		{
			// Per-chat "all" holds BOTH the broadcast and its own mention
			// topic: mention-adding edits publish only the mention topics
			// (no room re-notify), so chats/<sha> alone would drop them.
			name:      "one none override flips the whole space to per-chat",
			spaceMode: ModeAll,
			chats:     []chatNotify{{objectId: "c1", mode: ModeNone}, {objectId: "c2"}},
			topics: []string{ // c1 muted, c2 inherits all
				TopicChats + "/" + sha("c2"),
				TopicChats + "/" + sha("c2") + "/" + testIdentity,
			},
		},
		{
			name:      "mentions override yields the per-chat mention topic",
			spaceMode: ModeAll,
			chats:     []chatNotify{{objectId: "c1", mode: ModeMentions}, {objectId: "c2"}},
			topics: []string{
				TopicChats + "/" + sha("c1") + "/" + testIdentity,
				TopicChats + "/" + sha("c2"),
				TopicChats + "/" + sha("c2") + "/" + testIdentity,
			},
		},
		{
			name:      "inherit from space mentions mode",
			spaceMode: ModeMentions,
			chats:     []chatNotify{{objectId: "c1", mode: ModeAll}, {objectId: "c2"}},
			topics: []string{
				TopicChats + "/" + sha("c1"),
				TopicChats + "/" + sha("c1") + "/" + testIdentity,
				TopicChats + "/" + sha("c2") + "/" + testIdentity,
			},
		},
		{
			name:      "garbage mode inherits inside a per-chat space",
			spaceMode: ModeAll,
			chats:     []chatNotify{{objectId: "c1", mode: "loud"}, {objectId: "c2", mode: ModeNone}},
			topics: []string{
				TopicChats + "/" + sha("c1"),
				TopicChats + "/" + sha("c1") + "/" + testIdentity,
			},
		},
		{
			name:      "all override un-mutes a chat in a muted space",
			spaceMode: ModeNone,
			chats:     []chatNotify{{objectId: "c1", mode: ModeAll}, {objectId: "c2"}},
			topics: []string{ // c2 inherits none
				TopicChats + "/" + sha("c1"),
				TopicChats + "/" + sha("c1") + "/" + testIdentity,
			},
		},
		{
			name:      "every chat muted → no subscription entry at all",
			spaceMode: ModeAll,
			chats:     []chatNotify{{objectId: "c1", mode: ModeNone}, {objectId: "c2", mode: ModeNone}},
			topics:    nil,
		},
		{
			name:      "absent space mode defaults to all in the per-chat branch",
			spaceMode: "",
			chats:     []chatNotify{{objectId: "c1", mode: ModeMentions}, {objectId: "c2"}},
			topics: []string{
				TopicChats + "/" + sha("c1") + "/" + testIdentity,
				TopicChats + "/" + sha("c2"),
				TopicChats + "/" + sha("c2") + "/" + testIdentity,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := activeSpace("sp1", tc.spaceMode)
			subs, _ := desiredSubs([]space.SpaceInfo{info},
				map[string][]chatNotify{"sp1": tc.chats}, testIdentity)
			if tc.topics == nil {
				if len(subs) != 0 {
					t.Fatalf("want no subs, got %+v", subs)
				}
				return
			}
			if len(subs) != 1 || subs[0].SpaceId != "sp1" {
				t.Fatalf("subs = %+v", subs)
			}
			if !reflect.DeepEqual(subs[0].Topics, tc.topics) {
				t.Errorf("topics = %v, want %v", subs[0].Topics, tc.topics)
			}
		})
	}
}

// TestPerChatAll_SubscribesEditPublishTopics pins the preference
// ordering across the subscribe/publish split: NotifyChatEdit
// publishes a mention-adding edit ONLY on the per-chat mention + bare
// identity topics (no room re-notify), so an effective-"all" chat in
// the per-chat branch must subscribe a topic from that publish set —
// otherwise "all" users would miss edits that "mentions" users get.
func TestPerChatAll_SubscribesEditPublishTopics(t *testing.T) {
	// c1's none override flips the space per-chat; c2 is effective-all.
	subs := spaceTopics(ModeAll,
		[]chatNotify{{objectId: "c1", mode: ModeNone}, {objectId: "c2"}}, testIdentity)
	// NotifyChatEdit's publish set for a mention of testIdentity in c2
	// (chatpush.go): per-chat mention topic + bare identity.
	published := map[string]bool{
		TopicChats + "/" + sha("c2") + "/" + testIdentity: true,
		testIdentity: true,
	}
	for _, topic := range subs {
		if published[topic] {
			return
		}
	}
	t.Fatalf("effective-all chat subscribes %v — none of the edit publish topics %v; mention-adding edits would be dropped", subs, published)
}

// TestDesiredSubs_PerChatScopedToSpace: an override in one space never
// leaks per-chat behavior into another.
func TestDesiredSubs_PerChatScopedToSpace(t *testing.T) {
	infos := []space.SpaceInfo{activeSpace("spA", ModeAll), activeSpace("spB", ModeAll)}
	chats := map[string][]chatNotify{
		"spA": {{objectId: "c1", mode: ModeNone}},
		"spB": {{objectId: "c2"}}, // no override → bulk
	}
	subs, _ := desiredSubs(infos, chats, testIdentity)
	if len(subs) != 1 {
		t.Fatalf("subs = %+v, want only spB (spA fully muted per-chat)", subs)
	}
	if subs[0].SpaceId != "spB" || !reflect.DeepEqual(subs[0].Topics, []string{TopicChats, testIdentity}) {
		t.Errorf("spB should stay bulk: %+v", subs[0])
	}
}

func TestDesiredSubs_SkipsNonActive(t *testing.T) {
	infos := []space.SpaceInfo{
		activeSpace("active", ModeAll),
		{Id: "deleted", Status: space.StatusDeleted, OwnRole: space.PermissionOwner},
		{Id: "pending", Status: space.StatusOneToOnePending, SpaceType: space.SpaceTypeOneToOne},
		{Id: "declined", Status: space.StatusOneToOneDeclined, SpaceType: space.SpaceTypeOneToOne},
		{Id: "joining", Status: space.StatusJoining},
		{Id: "invitePending", Status: space.StatusInvitePending, OwnRole: space.PermissionWriter},
	}
	subs, register := desiredSubs(infos, nil, testIdentity)
	if len(subs) != 1 || subs[0].SpaceId != "active" {
		t.Errorf("only the active space should subscribe: %+v", subs)
	}
	if len(register) != 0 {
		t.Errorf("non-active rows must not register: %v", register)
	}
}

func TestDesiredSubs_Register(t *testing.T) {
	owned := activeSpace("owned", ModeAll)
	owned.OwnRole = space.PermissionOwner
	oneToOne := activeSpace("one2one", ModeAll)
	oneToOne.SpaceType = space.SpaceTypeOneToOne
	member := activeSpace("member", ModeAll) // writer, regular → no register
	// Muted spaces still register: the derived key serves every
	// member's subscriptions, not just ours.
	mutedOwned := activeSpace("mutedOwned", ModeNone)
	mutedOwned.OwnRole = space.PermissionOwner

	subs, register := desiredSubs([]space.SpaceInfo{owned, oneToOne, member, mutedOwned}, nil, testIdentity)
	if want := []string{"mutedOwned", "one2one", "owned"}; !reflect.DeepEqual(register, want) {
		t.Errorf("register = %v, want %v", register, want)
	}
	if len(subs) != 3 { // mutedOwned contributes no topics
		t.Errorf("subs = %+v", subs)
	}
}

func TestDesiredHash_StableAndSensitive(t *testing.T) {
	a := []space.SpaceInfo{activeSpace("sp1", ModeAll), activeSpace("sp2", ModeMentions)}
	// Same state, different list order → same hash (desiredSubs sorts).
	b := []space.SpaceInfo{activeSpace("sp2", ModeMentions), activeSpace("sp1", ModeAll)}

	subsA, regA := desiredSubs(a, nil, testIdentity)
	subsB, regB := desiredSubs(b, nil, testIdentity)
	if desiredHash(regA, subsA) != desiredHash(regB, subsB) {
		t.Error("hash should be order-independent")
	}

	// A mode flip changes the hash.
	c := []space.SpaceInfo{activeSpace("sp1", ModeNone), activeSpace("sp2", ModeMentions)}
	subsC, regC := desiredSubs(c, nil, testIdentity)
	if desiredHash(regA, subsA) == desiredHash(regC, subsC) {
		t.Error("hash should change when a space's mode changes")
	}

	// A register-set change alone changes the hash too.
	d := []space.SpaceInfo{activeSpace("sp1", ModeAll), activeSpace("sp2", ModeMentions)}
	d[0].OwnRole = space.PermissionOwner
	subsD, regD := desiredSubs(d, nil, testIdentity)
	if desiredHash(regA, subsA) == desiredHash(regD, subsD) {
		t.Error("hash should change when the register set changes")
	}

	// A per-chat override flip alone changes the hash (the sync loop's
	// re-subscribe trigger when only chat.notifyMode moved).
	subsE, regE := desiredSubs(a, map[string][]chatNotify{
		"sp1": {{objectId: "c1", mode: ModeNone}},
	}, testIdentity)
	if desiredHash(regA, subsA) == desiredHash(regE, subsE) {
		t.Error("hash should change when a chat's override changes")
	}

	if desiredHash(nil, nil) == "" {
		t.Error("hash must never be empty (the never-synced sentinel)")
	}
}
