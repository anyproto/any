package markdown

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"

	"github.com/anyproto/any/internal/editor"
)

func renderMarkdown(md string) string {
	parts := Split(md)
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = RenderBlock(ParseBlock(p))
	}
	return Join(out)
}

func html(t *testing.T, md string) string {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, goldmark.Convert([]byte(md), &out))
	return out.String()
}

// An ordered item keeps its number, and its continuation lines keep
// their place under the marker.
func TestListNumberRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 9, 10, 100, 999999999} {
		marker := fmt.Sprintf("%d. ", n)
		pad := strings.Repeat(" ", len(marker))
		in := marker + "Parent\n" + pad + "continued\n" + pad + "  deliberate spaces\n" + pad + "- Child\n" + pad + "  child continuation"
		require.Equal(t, in, renderMarkdown(in))
		require.Equal(t, in, renderMarkdown(renderMarkdown(in)))
	}
	for _, in := range []string{
		"1. a\n\n2. b\n\n3. c",
		"9. Parent\n   continued\n\n10. Next\n    continued",
		"10. Restart the computer.\n\n    Wait until the login screen appears.",
	} {
		require.Equal(t, in, renderMarkdown(in))
	}
}

func TestListNumberStyle(t *testing.T) {
	require.Equal(t, map[string]any{editor.StyleOrdered: true, editor.StyleNumber: 10}, ParseBlock("10. a").Style)
	require.Equal(t, map[string]any{editor.StyleOrdered: true}, ParseBlock("1. a").Style)
	require.Equal(t, map[string]any{editor.StyleOrdered: false}, ParseBlock("- a").Style)
}

// A marker written wider than it renders (extra spacing, indentation,
// leading zeros) moves its continuation lines left by the difference.
func TestListContinuationFollowsMarker(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"10.  a\n     b", "10. a\n    b"},
		{"  010) a\n       b\n         - c", "10. a\n    b\n      - c"},
		{"1.\tfoo\n\tbar", "1. foo\n   bar"},
		{"  - a\n    b", "- a\n  b"},
		{"-  [x] a\n   b", "- [x] a\n  b"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got := renderMarkdown(tc.in)
			require.Equal(t, tc.want, got)
			require.Equal(t, got, renderMarkdown(got))
			require.Equal(t, html(t, tc.in), html(t, got))
		})
	}
}

// Records without a number render as before.
func TestListNumberMissing(t *testing.T) {
	for _, style := range []map[string]any{
		{editor.StyleOrdered: true},
		{editor.StyleOrdered: true, editor.StyleNumber: -1},
		{editor.StyleOrdered: true, editor.StyleNumber: "10"},
	} {
		b := ParsedBlock{Type: editor.TypeListItem, Style: style, Text: "a\n    b"}
		require.Equal(t, "1. a\n    b", RenderBlock(b))
	}
	b := ParsedBlock{Type: editor.TypeListItem, Style: map[string]any{editor.StyleOrdered: false, editor.StyleNumber: 10}, Text: "a"}
	require.Equal(t, "- a", RenderBlock(b))
}
