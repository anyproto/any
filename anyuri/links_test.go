package anyuri_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/any/anyuri"
)

func TestExtractLinks(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string // u.String() of each hit, in order
	}{
		{"empty", "", nil},
		{"no links", "plain text with http://x.io and mailto:a@b", nil},
		{"bare in-space", "see any://obj1abc for details", []string{"any://obj1abc"}},
		{"bare global", "see any://space1/obj1abc", []string{"any://space1/obj1abc"}},
		{"typed object", "[spec](any://o/space1/obj1abc)", []string{"any://o/space1/obj1abc"}},
		{"record path", "[blk](any://o/space1/obj1abc/editor_blocks/blk9)", []string{"any://o/space1/obj1abc/editor_blocks/blk9"}},
		{"mention", "hey [Zarko](any://m/space1/ident1)", []string{"any://m/space1/ident1"}},
		{"prop", "from any://p/space1/obj1abc/prop1", []string{"any://p/space1/obj1abc/prop1"}},
		{"file with variant", "![x](any://f/space1/file1x?variant=thumb)", []string{"any://f/space1/file1x?variant=thumb"}},
		{"space", "go to any://s/space1", []string{"any://s/space1"}},
		{"fragment kept", "[x](any://o/space1/obj1abc#turn_3)", []string{"any://o/space1/obj1abc#turn_3"}},
		{"unknown kind skipped", "join any://i/space1/inviteX please", nil},
		{"malformed skipped", "any://o/space1 and any:// and any://o/a/b/c", nil},
		{"dedup", "any://o/space1/obj1abc twice any://o/space1/obj1abc", []string{"any://o/space1/obj1abc"}},
		{"order and mix", "[a](any://m/space1/ident1) then any://o/space1/obj1abc and [f](any://f/space1/file1x)",
			[]string{"any://m/space1/ident1", "any://o/space1/obj1abc", "any://f/space1/file1x"}},
		{"trailing punctuation", "right, any://o/space1/obj1abc.", []string{"any://o/space1/obj1abc"}},
		{"glued period on object", "see any://o/space1/obj1abc.Next word", []string{"any://o/space1/obj1abc"}},
		{"glued period on record", "see any://o/space1/obj1abc/editor_blocks/blk9.Next", []string{"any://o/space1/obj1abc/editor_blocks/blk9"}},
		{"glued period on bare", "see any://obj1abc.Next", []string{"any://obj1abc"}},
		{"dotted space id kept", "[x](any://o/bafyspace.1replkey/obj1abc)", []string{"any://o/bafyspace.1replkey/obj1abc"}},
		{"glued colon", "any://o/space1/obj1abc:said", []string{"any://o/space1/obj1abc"}},
		{"longer scheme ignored", "see many://o/space1/obj1abc there", nil},
		{"quoted and bracketed", `"any://o/space1/obj1abc" <any://o/space1/obj2abc>`, []string{"any://o/space1/obj1abc", "any://o/space1/obj2abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := anyuri.ExtractLinks(tc.text)
			var strs []string
			for _, u := range got {
				strs = append(strs, u.String())
			}
			assert.Equal(t, tc.want, strs)
		})
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct {
		in, space, want string
		ok              bool
	}{
		{"any://obj1abc", "space1", "any://o/space1/obj1abc", true},
		{"any://obj1abc", "", "", false},
		{"any://space2/obj1abc", "space1", "any://o/space2/obj1abc", true},
		{"any://o/space2/obj1abc#frag", "space1", "any://o/space2/obj1abc", true},
		{"any://o/space2/obj1abc/editor_blocks/blk9", "space1", "any://o/space2/obj1abc/editor_blocks/blk9", true},
		{"any://p/space2/obj1abc/prop1", "space1", "any://p/space2/obj1abc/prop1", true},
		{"any://m/space2/ident1", "space1", "any://m/space2/ident1", true},
		{"any://f/space2/file1x?variant=thumb", "space1", "any://f/space2/file1x", true},
		{"any://s/space2", "space1", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			u, err := anyuri.Parse(tc.in)
			require.NoError(t, err)
			c, ok := u.Canonical(tc.space)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.want, c.String())
			}
		})
	}
}

func TestObjectKeyAndIsPart(t *testing.T) {
	obj, _ := anyuri.Parse("any://o/space1/obj1abc")
	rec, _ := anyuri.Parse("any://o/space1/obj1abc/editor_blocks/blk9")
	prop, _ := anyuri.Parse("any://p/space1/obj1abc/prop1")
	men, _ := anyuri.Parse("any://m/space1/ident1")
	file, _ := anyuri.Parse("any://f/space1/file1x")

	for _, u := range []anyuri.URI{obj, rec, prop} {
		k, ok := u.ObjectKey()
		require.True(t, ok)
		assert.Equal(t, "any://o/space1/obj1abc", k)
	}
	for _, u := range []anyuri.URI{men, file} {
		_, ok := u.ObjectKey()
		assert.False(t, ok)
	}
	assert.False(t, obj.IsPart())
	assert.True(t, rec.IsPart())
	assert.True(t, prop.IsPart())
	assert.False(t, men.IsPart())
}
