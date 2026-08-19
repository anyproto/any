package chat

import (
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
)

// TestGeneralChatInstaller pins the sole-installer rule: exactly one
// member of a space installs its general chat, and for a 1-1 — where
// the ACL names no owner — the two sides must reach opposite answers
// from the same comparison.
func TestGeneralChatInstaller(t *testing.T) {
	const (
		low  = "aaa-identity"
		high = "zzz-identity"
	)

	cases := []struct {
		name    string
		info    space.SpaceInfo
		account string
		want    bool
	}{
		{
			name:    "owner of an ordinary space installs",
			info:    space.SpaceInfo{Author: low, OwnRole: space.PermissionOwner},
			account: low,
			want:    true,
		},
		{
			name:    "owner installs before ownRole is mirrored",
			info:    space.SpaceInfo{Author: low},
			account: low,
			want:    true,
		},
		{
			name:    "owner installs before the acl resolves an author",
			info:    space.SpaceInfo{OwnRole: space.PermissionOwner},
			account: low,
			want:    true,
		},
		{
			name:    "joiner adopts",
			info:    space.SpaceInfo{Author: high, OwnRole: space.PermissionWriter},
			account: low,
			want:    false,
		},
		{
			name:    "one-to-one: lower identity installs",
			info:    space.SpaceInfo{SpaceType: space.SpaceTypeOneToOne, Author: high, OwnRole: space.PermissionWriter},
			account: low,
			want:    true,
		},
		{
			name:    "one-to-one: higher identity adopts",
			info:    space.SpaceInfo{SpaceType: space.SpaceTypeOneToOne, Author: low, OwnRole: space.PermissionWriter},
			account: high,
			want:    false,
		},
		{
			name:    "one-to-one: unknown peer waits",
			info:    space.SpaceInfo{SpaceType: space.SpaceTypeOneToOne, OwnRole: space.PermissionWriter},
			account: low,
			want:    false,
		},
		{
			name:    "unknown account never installs",
			info:    space.SpaceInfo{Author: low, OwnRole: space.PermissionOwner},
			account: "",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := generalChatInstaller(tc.info, tc.account); got != tc.want {
				t.Fatalf("generalChatInstaller(%+v, %q) = %v, want %v", tc.info, tc.account, got, tc.want)
			}
		})
	}

	// The 1-1 rule must be antisymmetric — exactly one of the pair says
	// yes, whichever side is asking.
	a := space.SpaceInfo{SpaceType: space.SpaceTypeOneToOne, Author: high}
	b := space.SpaceInfo{SpaceType: space.SpaceTypeOneToOne, Author: low}
	if generalChatInstaller(a, low) == generalChatInstaller(b, high) {
		t.Fatal("both sides of a 1-1 reached the same verdict")
	}
}
