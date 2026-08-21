package markdown

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplit_Empty(t *testing.T) {
	assert.Nil(t, Split(""))
	// A blank-only document is a run of empty paragraphs, not an empty
	// document: splitLines eats one trailing newline as the terminator
	// and every remaining blank line is a paragraph.
	assert.Equal(t, []string{"", "", ""}, Split("\n\n\n"))
	assert.Equal(t, []string{"", ""}, Split("   \n\t\n"))
	assert.Equal(t, []string{""}, Split("\n"))
}

func TestSplit_Paragraphs(t *testing.T) {
	out := Split("alpha\n\nbeta\n\ngamma\n")
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, out)
}

func TestSplit_ParagraphMultiline(t *testing.T) {
	in := "first line\nsecond line\n\nnext para\n"
	out := Split(in)
	assert.Equal(t, []string{"first line\nsecond line", "next para"}, out)
}

func TestSplit_ATXHeadings(t *testing.T) {
	in := "# H1\n## H2\n### H3\n\nparagraph\n"
	out := Split(in)
	assert.Equal(t, []string{"# H1", "## H2", "### H3", "paragraph"}, out)
}

func TestSplit_SetextHeading(t *testing.T) {
	in := "Title\n=====\n\nbody\n"
	out := Split(in)
	assert.Equal(t, []string{"Title\n=====", "body"}, out)
}

func TestSplit_SetextDoesNotEatBlockquote(t *testing.T) {
	in := "> quoted\n=====\n"
	out := Split(in)
	assert.Equal(t, []string{"> quoted", "====="}, out)
}

func TestSplit_FencedCodeBacktick(t *testing.T) {
	in := "before\n\n```go\nfunc x() {}\n```\n\nafter\n"
	out := Split(in)
	require.Len(t, out, 3)
	assert.Equal(t, "before", out[0])
	assert.Equal(t, "```go\nfunc x() {}\n```", out[1])
	assert.Equal(t, "after", out[2])
}

func TestSplit_FencedCodeTilde(t *testing.T) {
	in := "~~~\nplain\n~~~\n"
	out := Split(in)
	assert.Equal(t, []string{"~~~\nplain\n~~~"}, out)
}

func TestSplit_FencedCodeUnterminated(t *testing.T) {
	in := "```\nstill open\n"
	out := Split(in)
	assert.Equal(t, []string{"```\nstill open"}, out)
}

func TestSplit_FencedCodePreservesBlankLines(t *testing.T) {
	in := "```\nline1\n\nline3\n```\n"
	out := Split(in)
	assert.Equal(t, []string{"```\nline1\n\nline3\n```"}, out)
}

func TestSplit_Blockquote(t *testing.T) {
	in := "> one\n> two\n> three\n\nafter\n"
	out := Split(in)
	assert.Equal(t, []string{"> one\n> two\n> three", "after"}, out)
}

func TestSplit_BlockquoteWithEmptyMarkerLine(t *testing.T) {
	in := "> p1\n>\n> p2\n"
	out := Split(in)
	assert.Equal(t, []string{"> p1\n>\n> p2"}, out)
}

func TestSplit_UnorderedListPerItem(t *testing.T) {
	in := "- a\n- b\n- c\n"
	out := Split(in)
	assert.Equal(t, []string{"- a", "- b", "- c"}, out)
}

func TestSplit_OrderedListPerItem(t *testing.T) {
	in := "1. a\n2. b\n3. c\n"
	out := Split(in)
	assert.Equal(t, []string{"1. a", "2. b", "3. c"}, out)
}

func TestSplit_ListItemContinuation(t *testing.T) {
	in := "- first item\n  continuation\n- second item\n"
	out := Split(in)
	assert.Equal(t, []string{"- first item\n  continuation", "- second item"}, out)
}

func TestSplit_NestedListInsideItem(t *testing.T) {
	in := "- parent\n  - child a\n  - child b\n- next parent\n"
	out := Split(in)
	assert.Equal(t, []string{"- parent\n  - child a\n  - child b", "- next parent"}, out)
}

func TestSplit_HeadingsAndParagraphsInterleaved(t *testing.T) {
	in := "# H\nbody\n## H2\nmore body\n"
	out := Split(in)
	assert.Equal(t, []string{"# H", "body", "## H2", "more body"}, out)
}

func TestSplit_HTMLBlock(t *testing.T) {
	in := "<div>\n  hello\n</div>\n\nafter\n"
	out := Split(in)
	assert.Equal(t, []string{"<div>\n  hello\n</div>", "after"}, out)
}

func TestSplit_TableOpaque(t *testing.T) {
	in := "| a | b |\n| - | - |\n| 1 | 2 |\n\nafter\n"
	out := Split(in)
	assert.Equal(t, []string{"| a | b |\n| - | - |\n| 1 | 2 |", "after"}, out)
}

func TestSplit_BlankLinesCarryEmptyParagraphs(t *testing.T) {
	// Leading run: 3 lines, no separator duty  -> 3 empty paragraphs.
	// Between:     4 lines, one is the separator -> 3.
	// Trailing:    3 lines minus the terminator, no duty -> 2.
	in := "\n\n\nalpha\n\n\n\nbeta\n\n\n"
	out := Split(in)
	assert.Equal(t, []string{"", "", "", "alpha", "", "", "beta", "", ""}, out)
}

func TestSplit_SingleBlankLineStaysASeparator(t *testing.T) {
	// The shape every existing document is stored in must be unchanged
	// by the empty-paragraph encoding.
	assert.Equal(t, []string{"alpha", "beta"}, Split("alpha\n\nbeta"))
	assert.Equal(t, []string{"alpha", "beta"}, Split("alpha\n\nbeta\n"))
	assert.Equal(t, []string{"# H1", "body"}, Split("# H1\n\nbody\n"))
}

func TestSplit_EmptyParagraphBetweenListItems(t *testing.T) {
	// The reported list case: a blank line beyond the separator between
	// two items is content, not formatting.
	out := Split("- one\n\n\n- two\n")
	assert.Equal(t, []string{"- one", "", "- two"}, out)
}

func TestSplit_BlanksInsideBlocksAreNotEmptyParagraphs(t *testing.T) {
	// Blank lines a block consumed itself never reach the top level.
	assert.Equal(t, []string{"```\n\n\n```"}, Split("```\n\n\n```\n"))
	assert.Equal(t, []string{"> a\n>\n> b"}, Split("> a\n>\n> b\n"))
	assert.Equal(t, []string{"- item\n\n  continued"}, Split("- item\n\n  continued\n"))
}

func TestSplit_CRLF(t *testing.T) {
	in := "alpha\r\n\r\nbeta\r\n"
	out := Split(in)
	assert.Equal(t, []string{"alpha", "beta"}, out)
}

func TestJoin_RoundTripIdempotent(t *testing.T) {
	cases := []string{
		"alpha\n\nbeta\n",
		"# H1\n\n## H2\n\nbody\n",
		"```go\nfunc x() {}\n```\n\nafter\n",
		"> a\n> b\n\nafter\n",
		"- a\n\n- b\n\n- c\n",
		"1. one\n\n2. two\n",
		"<div>\n  x\n</div>\n\nbody\n",
		"first line\nsecond line\n\nthird\n",
	}
	for _, in := range cases {
		t.Run(strings.SplitN(in, "\n", 2)[0], func(t *testing.T) {
			blocks1 := Split(in)
			joined := Join(blocks1)
			blocks2 := Split(joined)
			assert.Equal(t, blocks1, blocks2,
				"split→join→split must be idempotent\nfirst:  %q\njoined: %q", blocks1, joined)
		})
	}
}

func TestJoin_EmptyParagraphs(t *testing.T) {
	assert.Equal(t, "", Join(nil))
	assert.Equal(t, "", Join([]string{}))
	assert.Equal(t, "a\n\nb", Join([]string{"a", "b"}))
	// One extra newline per empty paragraph between two blocks.
	assert.Equal(t, "a\n\n\nb", Join([]string{"a", "", "b"}))
	assert.Equal(t, "a\n\n\n\nb", Join([]string{"a", "", "", "b"}))
	// Leading: no separator to build on, one newline each.
	assert.Equal(t, "\na", Join([]string{"", "a"}))
	assert.Equal(t, "\n\na", Join([]string{"", "", "a"}))
	// Trailing: one extra newline so splitLines' terminator has
	// something to eat.
	assert.Equal(t, "a\n\n", Join([]string{"a", ""}))
	assert.Equal(t, "a\n\n\n", Join([]string{"a", "", ""}))
	// No content at all: no terminator allowance, one newline each.
	assert.Equal(t, "\n", Join([]string{""}))
	assert.Equal(t, "\n\n", Join([]string{"", ""}))
}

func TestSplitJoin_EmptyParagraphRoundTrip(t *testing.T) {
	// Split(Join(b)) == b for every arrangement of empties around
	// content — the invariant the client's encoding is written against.
	cases := [][]string{
		nil,
		{"a"},
		{"a", "b"},
		{"a", "", "b"},
		{"a", "", "", "b"},
		{"", "a"},
		{"", "", "a"},
		{"a", ""},
		{"a", "", ""},
		{""},
		{"", ""},
		{"", "a", "", "b", ""},
		{"# H1", "", "para", "", "", "- item"},
		{"```go\nfunc x() {}\n```", "", "after"},
	}
	for _, blocks := range cases {
		t.Run(strings.Join(blocks, "|"), func(t *testing.T) {
			joined := Join(blocks)
			got := Split(joined)
			if len(blocks) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, blocks, got, "joined: %q", joined)
		})
	}
}

func TestTrimEdgeEmpties(t *testing.T) {
	assert.Equal(t, []string{"a", "", "b"},
		trimEdgeEmpties([]string{"", "", "a", "", "b", "", ""}))
	assert.Empty(t, trimEdgeEmpties([]string{"", ""}))
	assert.Empty(t, trimEdgeEmpties(nil))
	assert.Equal(t, []string{"a"}, trimEdgeEmpties([]string{"a"}))
}

func TestSplit_UnterminatedFenceKeepsTrailingEmptyParagraphs(t *testing.T) {
	// An unterminated fence runs to EOF, but trailing blank lines are
	// the document's, not the code block's — otherwise the round-trip
	// invariant breaks and the body silently grows newlines.
	blocks := []string{"```\nx", ""}
	joined := Join(blocks)
	assert.Equal(t, "```\nx\n\n", joined)
	assert.Equal(t, blocks, Split(joined))
	// A terminated fence still keeps everything between its markers.
	assert.Equal(t, []string{"```\n\n\n```"}, Split("```\n\n\n```\n"))
}

func TestSplitJoin_RoundTripFuzz(t *testing.T) {
	// Split(Join(b)) == b over generated documents: the property the
	// client's serialize/parse pair is written against. Fixed seed —
	// a failure must be reproducible.
	vocab := []string{
		"", "alpha", "# heading", "- item", "1. one", "> quote",
		"```go\nfunc x() {}\n```", "| a | b |", "<div>x</div>",
		"first line\nsecond line", "---", "- [ ] todo",
	}
	rng := rand.New(rand.NewSource(164))
	for i := 0; i < 5000; i++ {
		blocks := make([]string, rng.Intn(6))
		for j := range blocks {
			blocks[j] = vocab[rng.Intn(len(vocab))]
		}
		joined := Join(blocks)
		got := Split(joined)
		if len(blocks) == 0 {
			require.Empty(t, got, "case %d: %q", i, joined)
			continue
		}
		require.Equal(t, blocks, got, "case %d: joined %q", i, joined)
	}
}
