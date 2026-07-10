package anyuri_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/any/anyuri"
)

// Fixture ids are ≥5 chars on purpose: 1-4 lowercase-alphanumeric
// first segments are the reserved kind-slug namespace.

func TestTypedRoundTrip(t *testing.T) {
	cases := []struct {
		text string
		want anyuri.URI
	}{
		{"any://o/space1/object1",
			anyuri.URI{Kind: anyuri.KindObject, SpaceId: "space1", ObjectId: "object1"}},
		{"any://o/space1/object1/chat_messages/msgid1",
			anyuri.URI{Kind: anyuri.KindObject, SpaceId: "space1", ObjectId: "object1", Dataset: "chat_messages", RecordId: "msgid1"}},
		{"any://o/space1/object1/editor_blocks/block1",
			anyuri.URI{Kind: anyuri.KindObject, SpaceId: "space1", ObjectId: "object1", Dataset: "editor_blocks", RecordId: "block1"}},
		{"any://m/space1/ident1",
			anyuri.URI{Kind: anyuri.KindMention, SpaceId: "space1", Identity: "ident1"}},
		{"any://s/space1",
			anyuri.URI{Kind: anyuri.KindSpace, SpaceId: "space1"}},
		{"any://p/space1/object1/prop1x",
			anyuri.URI{Kind: anyuri.KindProp, SpaceId: "space1", ObjectId: "object1", PropId: "prop1x"}},
		{"any://f/space1/file1x",
			anyuri.URI{Kind: anyuri.KindFile, SpaceId: "space1", FileId: "file1x"}},
		{"any://f/space1/file1x?variant=thumb",
			anyuri.URI{Kind: anyuri.KindFile, SpaceId: "space1", FileId: "file1x", Variant: "thumb"}},
		{"any://o/space1/object1#turn_3",
			anyuri.URI{Kind: anyuri.KindObject, SpaceId: "space1", ObjectId: "object1", Fragment: "turn_3"}},
		{"any://f/space1/file1x?variant=thumb#top",
			anyuri.URI{Kind: anyuri.KindFile, SpaceId: "space1", FileId: "file1x", Variant: "thumb", Fragment: "top"}},
	}
	for _, tc := range cases {
		u, err := anyuri.Parse(tc.text)
		require.NoError(t, err, tc.text)
		assert.Equal(t, tc.want, u, tc.text)
		assert.Equal(t, tc.text, u.String(), "String must round-trip %q", tc.text)
		assert.True(t, anyuri.IsValid(tc.text), tc.text)
	}
}

func TestLegacyRoundTrip(t *testing.T) {
	cases := []struct {
		text string
		want anyuri.URI
	}{
		{"any://object1", anyuri.URI{Kind: anyuri.KindObject, Legacy: true, ObjectId: "object1"}},
		{"any://space1/object1", anyuri.URI{Kind: anyuri.KindObject, Legacy: true, SpaceId: "space1", ObjectId: "object1"}},
		{"any://object1#turn_3", anyuri.URI{Kind: anyuri.KindObject, Legacy: true, ObjectId: "object1", Fragment: "turn_3"}},
		{"any://space1/object1#turn_3", anyuri.URI{Kind: anyuri.KindObject, Legacy: true, SpaceId: "space1", ObjectId: "object1", Fragment: "turn_3"}},
	}
	for _, tc := range cases {
		u, err := anyuri.Parse(tc.text)
		require.NoError(t, err, tc.text)
		assert.Equal(t, tc.want, u, tc.text)
		assert.Equal(t, tc.text, u.String(), "legacy String must re-render bare: %q", tc.text)
		assert.True(t, anyuri.IsValid(tc.text), tc.text)
	}
}

func TestBuilders(t *testing.T) {
	assert.Equal(t, "any://object1", anyuri.BuildObject("object1"))
	assert.Equal(t, "any://o/space1/object1", anyuri.BuildObjectGlobal("space1", "object1"))
	assert.Equal(t, "any://o/space1/object1/chat_messages/msgid1",
		anyuri.BuildRecord("space1", "object1", "chat_messages", "msgid1"))
	assert.Equal(t, "any://m/space1/ident1", anyuri.BuildMention("space1", "ident1"))
	assert.Equal(t, "any://s/space1", anyuri.BuildSpace("space1"))
	assert.Equal(t, "any://p/space1/object1/prop1x", anyuri.BuildProp("space1", "object1", "prop1x"))
	assert.Equal(t, "any://f/space1/file1x", anyuri.BuildFile("space1", "file1x"))
	assert.Equal(t, "any://f/space1/file1x?variant=thumb",
		anyuri.BuildFileVariant("space1", "file1x", "thumb"))

	// Every builder's output must parse back to a known kind.
	for _, s := range []string{
		anyuri.BuildObject("object1"),
		anyuri.BuildObjectGlobal("space1", "object1"),
		anyuri.BuildRecord("space1", "object1", "chat_messages", "msgid1"),
		anyuri.BuildMention("space1", "ident1"),
		anyuri.BuildSpace("space1"),
		anyuri.BuildProp("space1", "object1", "prop1x"),
		anyuri.BuildFile("space1", "file1x"),
		anyuri.BuildFileVariant("space1", "file1x", "thumb"),
	} {
		assert.True(t, anyuri.IsValid(s), s)
	}
}

func TestUnknownKindDegrades(t *testing.T) {
	// Well-formed typed URIs of an unrecognized kind — including the
	// reserved invite kind — are ErrKindUnknown (degrade to plain
	// link), NOT ErrInvalid (reject).
	for _, s := range []string{
		"any://i/space1",
		"any://i/space1/token1",
		"any://xyz/space1/object1",
		"any://ab12/space1",
	} {
		_, err := anyuri.Parse(s)
		require.Error(t, err, s)
		assert.True(t, errors.Is(err, anyuri.ErrKindUnknown), "%q must be ErrKindUnknown", s)
		assert.False(t, errors.Is(err, anyuri.ErrInvalid), "%q must NOT be ErrInvalid", s)
		assert.False(t, anyuri.IsValid(s), s)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"",
		"object1",
		"any:/object1",
		"http://object1",
		"any://",
		"any:///object1",
		"any://space1/",
		"any://#frag",
		"any://?variant=thumb",
		"any://space1/object1/extra1",          // legacy form has at most 2 segments
		"any://o/space1",                       // o wants 2 or 4 segments after the kind
		"any://o/space1/object1/chat_messages", // dangling dataset without recordId
		"any://o/space1/object1/chat_messages/msgid1/prop1x", // reserved record-field form
		"any://m/space1",         // mention without identity
		"any://s/space1/object1", // space takes no further segments
		"any://p/space1/object1", // prop wants 3
		"any://f/space1",         // file wants 2
		"any://o/space1//msgid1", // empty segment
		"any://o//object1",       // empty space id
	} {
		_, err := anyuri.Parse(bad)
		require.Error(t, err, "%q must not parse", bad)
		assert.True(t, errors.Is(err, anyuri.ErrInvalid), "%q error must wrap ErrInvalid", bad)
		assert.False(t, anyuri.IsValid(bad))
	}
}

func TestUnknownParamsIgnored(t *testing.T) {
	u, err := anyuri.Parse("any://f/space1/file1x?foo=bar&variant=thumb&baz=1")
	require.NoError(t, err)
	assert.Equal(t, "thumb", u.Variant)
	// Unknown params are dropped and do not round-trip.
	assert.Equal(t, "any://f/space1/file1x?variant=thumb", u.String())

	u, err = anyuri.Parse("any://o/space1/object1?foo=bar")
	require.NoError(t, err)
	assert.Equal(t, anyuri.URI{Kind: anyuri.KindObject, SpaceId: "space1", ObjectId: "object1"}, u)
	assert.Equal(t, "any://o/space1/object1", u.String())
}

func TestIsPropertyValueRef(t *testing.T) {
	assert.True(t, anyuri.IsPropertyValueRef("any://object1"))

	for _, bad := range []string{
		"any://space1/object1",   // global form
		"any://object1#frag1",    // fragment
		"any://o/space1/object1", // typed form
		"any://m/space1/ident1",  // typed form
		"any://s/space1",         // typed form
		"any://",                 // garbage
		"object1",                // no scheme
		"any://abcd",             // kind-slug namespace, not an id
	} {
		assert.False(t, anyuri.IsPropertyValueRef(bad), bad)
	}
}
