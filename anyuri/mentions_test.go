package anyuri_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/anyproto/any/anyuri"
)

func TestExtractMentions(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"empty", "", nil},
		{"no mentions", "plain text with any://space1/object1 and http://x.io", nil},
		{"raw token", "ping any://m/space1/ident1 please", []string{"ident1"}},
		{"commonmark destination", "Hey [Zarko](any://m/space1/ident1), take a look", []string{"ident1"}},
		{"multiple ordered", "[a](any://m/space1/ident1) then [b](any://m/space1/ident2)", []string{"ident1", "ident2"}},
		{"dedup keeps first", "any://m/space1/ident1 and again [x](any://m/space1/ident1)", []string{"ident1"}},
		{"end of string", "cc any://m/space1/ident1", []string{"ident1"}},
		{"trailing punctuation", "right, any://m/space1/ident1.", []string{"ident1"}},
		{"quoted", `he said "any://m/space1/ident1" loudly`, []string{"ident1"}},
		{"angle brackets", "<any://m/space1/ident1>", []string{"ident1"}},
		{"with fragment", "[x](any://m/space1/ident1#top)", []string{"ident1"}},
		{"with params", "[x](any://m/space1/ident1?foo=bar)", []string{"ident1"}},
		{"missing identity ignored", "bad any://m/space1 link", nil},
		{"empty tail ignored", "bad any://m/ link", nil},
		{"other kinds ignored", "[o](any://o/space1/object1) [f](any://f/space1/file1x)", nil},
		{"longer scheme ignored", "see many://m/space1/ident1 there", nil},
		{"mixed", "[a](any://m/space1/ident1) sends any://f/space1/file1x to any://m/space1/ident2", []string{"ident1", "ident2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, anyuri.ExtractMentions(tc.text))
		})
	}
}

func TestExtractMentionsLargeText(t *testing.T) {
	// Adversarial: mention at the very end of a ~32KB message.
	text := strings.Repeat("lorem ipsum any:// any://m/ m/ ", 1000) +
		"[end](any://m/space1/ident1)"
	assert.Equal(t, []string{"ident1"}, anyuri.ExtractMentions(text))
}
