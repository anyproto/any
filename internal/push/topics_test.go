package push

import (
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
			subs, _ := desiredSubs([]space.SpaceInfo{info}, testIdentity)
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

func TestDesiredSubs_SkipsNonActive(t *testing.T) {
	infos := []space.SpaceInfo{
		activeSpace("active", ModeAll),
		{Id: "deleted", Status: space.StatusDeleted, OwnRole: space.PermissionOwner},
		{Id: "pending", Status: space.StatusOneToOnePending, SpaceType: space.SpaceTypeOneToOne},
		{Id: "declined", Status: space.StatusOneToOneDeclined, SpaceType: space.SpaceTypeOneToOne},
		{Id: "joining", Status: space.StatusJoining},
		{Id: "invitePending", Status: space.StatusInvitePending, OwnRole: space.PermissionWriter},
	}
	subs, register := desiredSubs(infos, testIdentity)
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

	subs, register := desiredSubs([]space.SpaceInfo{owned, oneToOne, member, mutedOwned}, testIdentity)
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

	subsA, regA := desiredSubs(a, testIdentity)
	subsB, regB := desiredSubs(b, testIdentity)
	if desiredHash(regA, subsA) != desiredHash(regB, subsB) {
		t.Error("hash should be order-independent")
	}

	// A mode flip changes the hash.
	c := []space.SpaceInfo{activeSpace("sp1", ModeNone), activeSpace("sp2", ModeMentions)}
	subsC, regC := desiredSubs(c, testIdentity)
	if desiredHash(regA, subsA) == desiredHash(regC, subsC) {
		t.Error("hash should change when a space's mode changes")
	}

	// A register-set change alone changes the hash too.
	d := []space.SpaceInfo{activeSpace("sp1", ModeAll), activeSpace("sp2", ModeMentions)}
	d[0].OwnRole = space.PermissionOwner
	subsD, regD := desiredSubs(d, testIdentity)
	if desiredHash(regA, subsA) == desiredHash(regD, subsD) {
		t.Error("hash should change when the register set changes")
	}

	if desiredHash(nil, nil) == "" {
		t.Error("hash must never be empty (the never-synced sentinel)")
	}
}
