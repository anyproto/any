package markdown

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/any/internal/editor"
)

func TestParse_Paragraph(t *testing.T) {
	b := ParseBlock("alpha **bold**")
	assert.Equal(t, editor.TypeParagraph, b.Type)
	assert.Equal(t, "alpha **bold**", b.Text)
	assert.Empty(t, b.Style)
}

func TestParse_ATXHeading(t *testing.T) {
	cases := []struct {
		in       string
		level    int
		text     string
		wantType string
	}{
		{"# Title", 1, "Title", editor.TypeHeading},
		{"## Sub", 2, "Sub", editor.TypeHeading},
		{"### Three", 3, "Three", editor.TypeHeading},
		{"###### Six", 6, "Six", editor.TypeHeading},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			b := ParseBlock(tc.in)
			assert.Equal(t, tc.wantType, b.Type)
			assert.Equal(t, tc.level, intStyle(b.Style, editor.StyleLevel, 0))
			assert.Equal(t, tc.text, b.Text)
		})
	}
}

func TestParse_SetextHeading(t *testing.T) {
	b := ParseBlock("Title\n=====")
	assert.Equal(t, editor.TypeHeading, b.Type)
	assert.Equal(t, 1, intStyle(b.Style, editor.StyleLevel, 0))
	assert.Equal(t, "Title", b.Text)

	b = ParseBlock("Title\n-----")
	assert.Equal(t, editor.TypeHeading, b.Type)
	assert.Equal(t, 2, intStyle(b.Style, editor.StyleLevel, 0))
}

func TestParse_FencedCode(t *testing.T) {
	b := ParseBlock("```go\nfunc x() {}\n```")
	assert.Equal(t, editor.TypeCode, b.Type)
	assert.Equal(t, "go", stringStyle(b.Style, editor.StyleLang, ""))
	assert.Equal(t, "func x() {}", b.Text)

	b = ParseBlock("```\nplain\n```")
	assert.Equal(t, editor.TypeCode, b.Type)
	assert.Equal(t, "", stringStyle(b.Style, editor.StyleLang, ""))
	assert.Equal(t, "plain", b.Text)
}

func TestParse_Quote(t *testing.T) {
	b := ParseBlock("> one\n> two")
	assert.Equal(t, editor.TypeQuote, b.Type)
	assert.Equal(t, "one\ntwo", b.Text)
}

func TestParse_ListItem(t *testing.T) {
	b := ParseBlock("- foo")
	assert.Equal(t, editor.TypeListItem, b.Type)
	assert.Equal(t, false, boolStyle(b.Style, editor.StyleOrdered, false))
	assert.Equal(t, "foo", b.Text)

	b = ParseBlock("1. one")
	assert.Equal(t, editor.TypeListItem, b.Type)
	assert.Equal(t, true, boolStyle(b.Style, editor.StyleOrdered, false))
	assert.Equal(t, "one", b.Text)
}

func TestParse_CheckListItem(t *testing.T) {
	b := ParseBlock("- [ ] todo")
	assert.Equal(t, editor.TypeCheckListItem, b.Type)
	assert.Equal(t, false, boolStyle(b.Style, editor.StyleChecked, false))
	assert.Equal(t, "todo", b.Text)

	b = ParseBlock("- [x] done")
	assert.Equal(t, editor.TypeCheckListItem, b.Type)
	assert.Equal(t, true, boolStyle(b.Style, editor.StyleChecked, false))
	assert.Equal(t, "done", b.Text)
}

func TestParse_Divider(t *testing.T) {
	assert.Equal(t, editor.TypeDivider, ParseBlock("---").Type)
	assert.Equal(t, editor.TypeDivider, ParseBlock("***").Type)
	assert.Equal(t, editor.TypeDivider, ParseBlock("___").Type)
}

func TestParse_HTML(t *testing.T) {
	b := ParseBlock("<div>\n  hello\n</div>")
	assert.Equal(t, editor.TypeHTML, b.Type)
	assert.Equal(t, "<div>\n  hello\n</div>", b.Text)
}

func TestParse_Table(t *testing.T) {
	b := ParseBlock("| a | b |\n| - | - |")
	assert.Equal(t, editor.TypeTable, b.Type)
	assert.Equal(t, "| a | b |\n| - | - |", b.Text)
}

func TestRender_Paragraph(t *testing.T) {
	out := RenderBlock(ParsedBlock{Type: editor.TypeParagraph, Text: "alpha"})
	assert.Equal(t, "alpha", out)
}

func TestRender_Heading(t *testing.T) {
	out := RenderBlock(ParsedBlock{
		Type:  editor.TypeHeading,
		Style: map[string]any{editor.StyleLevel: 2},
		Text:  "Sub",
	})
	assert.Equal(t, "## Sub", out)
}

func TestRender_FencedCode(t *testing.T) {
	out := RenderBlock(ParsedBlock{
		Type:  editor.TypeCode,
		Style: map[string]any{editor.StyleLang: "go"},
		Text:  "func x() {}",
	})
	assert.Equal(t, "```go\nfunc x() {}\n```", out)
}

func TestRender_Quote(t *testing.T) {
	out := RenderBlock(ParsedBlock{Type: editor.TypeQuote, Text: "a\nb"})
	assert.Equal(t, "> a\n> b", out)
}

func TestRender_ListItem(t *testing.T) {
	out := RenderBlock(ParsedBlock{
		Type:  editor.TypeListItem,
		Style: map[string]any{editor.StyleOrdered: false},
		Text:  "foo",
	})
	assert.Equal(t, "- foo", out)

	out = RenderBlock(ParsedBlock{
		Type:  editor.TypeListItem,
		Style: map[string]any{editor.StyleOrdered: true},
		Text:  "one",
	})
	assert.Equal(t, "1. one", out)
}

func TestRender_CheckListItem(t *testing.T) {
	out := RenderBlock(ParsedBlock{
		Type:  editor.TypeCheckListItem,
		Style: map[string]any{editor.StyleChecked: false},
		Text:  "todo",
	})
	assert.Equal(t, "- [ ] todo", out)

	out = RenderBlock(ParsedBlock{
		Type:  editor.TypeCheckListItem,
		Style: map[string]any{editor.StyleChecked: true},
		Text:  "done",
	})
	assert.Equal(t, "- [x] done", out)
}

func TestRender_Divider(t *testing.T) {
	assert.Equal(t, "---", RenderBlock(ParsedBlock{Type: editor.TypeDivider}))
}

// TestRoundTrip_Canonical confirms the parse → render cycle is
// byte-identical for the canonical forms used by the markdown e2e
// suite (paragraphs, ATX headings, fenced code, quotes, list items).
func TestRoundTrip_Canonical(t *testing.T) {
	cases := []string{
		"alpha",
		"first line\nsecond line",
		"# Title",
		"## Sub",
		"### Three",
		"###### Six",
		"```go\nfunc x() {}\n```",
		"```\nplain\n```",
		"> single line",
		"> one\n> two",
		"- foo",
		"- foo bar",
		"1. one",
		"- [ ] todo",
		"- [x] done",
		"---",
		"<div>\n  hello\n</div>",
		"| a | b |\n| - | - |",
	}
	for _, in := range cases {
		t.Run(strings.SplitN(in, "\n", 2)[0], func(t *testing.T) {
			parsed := ParseBlock(in)
			out := RenderBlock(parsed)
			assert.Equal(t, in, out,
				"parse → render must round-trip canonical input\nin:  %q\nout: %q\nparsed: %+v",
				in, out, parsed)
		})
	}
}

// TestRoundTrip_Document confirms that splitting → parsing → rendering
// → joining a multi-block document reproduces the input verbatim for
// the canonical forms. This is the round-trip the markdown HTTP
// handler relies on.
func TestRoundTrip_Document(t *testing.T) {
	cases := []string{
		"alpha\n\nbeta\n\ngamma",
		"# H1\n\nbody\n\n## H2\n\nmore body",
		"```go\nfunc x() {}\n```\n\nafter",
		"> a\n> b\n\nafter",
		"- a\n\n- b\n\n- c",
	}
	for _, in := range cases {
		t.Run(strings.SplitN(in, "\n", 2)[0], func(t *testing.T) {
			raws := Split(in)
			rendered := make([]string, len(raws))
			for i, raw := range raws {
				rendered[i] = RenderBlock(ParseBlock(raw))
			}
			got := Join(rendered)
			require.Equal(t, in, got,
				"split → parse → render → join must round-trip\nin:  %q\nout: %q",
				in, got)
		})
	}
}
