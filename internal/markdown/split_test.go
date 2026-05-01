package markdown

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplit_Empty(t *testing.T) {
	assert.Nil(t, Split(""))
	assert.Nil(t, Split("\n\n\n"))
	assert.Nil(t, Split("   \n\t\n"))
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

func TestSplit_DropsBlankLines(t *testing.T) {
	in := "\n\n\nalpha\n\n\n\nbeta\n\n\n"
	out := Split(in)
	assert.Equal(t, []string{"alpha", "beta"}, out)
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

func TestJoin_DropsEmptyEntries(t *testing.T) {
	assert.Equal(t, "a\n\nb", Join([]string{"a", "", "b", ""}))
	assert.Equal(t, "", Join([]string{"", ""}))
	assert.Equal(t, "", Join(nil))
}
