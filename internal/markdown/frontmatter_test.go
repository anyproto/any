package markdown

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/any/internal/editor"
)

// renderContent is the document pipeline a markdown PUT stores and a GET
// returns: split, parse, render, join.
func renderContent(t *testing.T, content string) ([]ParsedBlock, string) {
	t.Helper()
	parsed, _, err := parseContent(content)
	require.NoError(t, err)
	return parsed, documentText(parsed)
}

// save plans a PUT of content over stored blocks the way Set does and
// returns the blocks it leaves, Set's saved document, and whether it
// writes nothing.
func save(t *testing.T, existing []existingBlock, content string) (stored []existingBlock, saved string, noop bool) {
	t.Helper()
	parsed, keys, err := parseContent(content)
	require.NoError(t, err)
	plan := planDiff(existing, parsed, keys)
	noop = len(plan.Deletes) == 0
	stored = make([]existingBlock, len(parsed))
	for i, op := range plan.NewSeq {
		if op.Kind == OpKeep {
			stored[i] = existing[op.OldIdx]
			continue
		}
		noop = false
		stored[i] = existingBlock{Type: parsed[i].Type, Style: parsed[i].Style, Text: parsed[i].Text}
	}
	return stored, documentText(parsed), noop
}

// rePut re-saves the GET of stored blocks: it returns that GET, whether
// the save writes nothing, and the blocks it leaves. The next GET always
// equals the save's own document.
func rePut(t *testing.T, existing []existingBlock) (string, bool, []existingBlock) {
	t.Helper()
	doc := existingDocument(existing)
	stored, saved, noop := save(t, existing, doc)
	require.Equal(t, saved, existingDocument(stored), "the next GET is the saved document")
	return doc, noop, stored
}

func TestFrontMatter_RoundTripsVerbatim(t *testing.T) {
	long := "notes: " + strings.Repeat("Owner-requested block chrome, ", 40)
	cases := map[string]string{
		"spec":                  "---\nstatus: shipped\nlast-verified: 2026-10-03 (`any` at 371194f0)\n" + long + "\n---\n\n# PR #145 — Optional editor block handle",
		"blank lines and lists": "---\ntitle: \"a: b\"\n\ntags:\n  - one\n  - two\n\nnotes: |\n  # not a heading\n  > not a quote\n---\n\nbody",
		"backticks":             "---\ncode: ```go inline```\nsep: a ~~~ b\n---",
		"only front matter":     "---\nk: v\n---",
		"empty":                 "---\n---\n\nbody",
		"quoted key":            "---\n\"title\": x\n---",
		"key without a value":   "---\ntags:\n  - a\n---",
		"top-level list items":  "---\ntags:\n- a\n- b\nsite.url: x\n---",
		"cyrillic key":          "---\nавтор: Иван\nтеги:\n  - тест\n---",
		"cjk key":               "---\n作者: 张三\n日付: 2026-11-12\n---",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			parsed, out := renderContent(t, in)
			assert.Equal(t, editor.TypeFrontMatter, parsed[0].Type)
			assert.Equal(t, in, out)
			lines := strings.Split(in, "\n")
			end, ok := frontMatterEnd(lines)
			require.True(t, ok)
			assert.Equal(t, strings.Join(lines[1:end-1], "\n"), parsed[0].Text, "text is the lines between the delimiters")
		})
	}
}

func TestFrontMatter_DotsCloserRendersDashes(t *testing.T) {
	parsed, out := renderContent(t, "---\ntitle: x\n...\n\nbody")
	assert.Equal(t, editor.TypeFrontMatter, parsed[0].Type)
	assert.Equal(t, "title: x", parsed[0].Text)
	assert.Equal(t, "---\ntitle: x\n---\n\nbody", out)
}

func TestFrontMatter_SplitCutsAtTheFirstCloser(t *testing.T) {
	got := Split("---\na: 1\n---\nbody text\n---\n")
	assert.Equal(t, []string{"---\na: 1\n---", "body text\n---"}, got)
}

// Off the first line, with a line between the delimiters that is not
// YAML-shaped, without a closer or in an appended fragment the lines read
// exactly as they do without the front matter rule.
func TestFrontMatter_OnlyAtTheStartAndClosed(t *testing.T) {
	cases := []struct {
		name, doc string
		want      []string
	}{
		{"not at the start", "para\n\n---\nfoo: bar\n---", []string{"para", "---\nfoo: bar\n---"}},
		{"blank line first", "\n---\nfoo: bar\n---", []string{"", "---\nfoo: bar\n---"}},
		{"blank line inside", "para\n\n---\nstatus: x\n\nnotes: y\n---", []string{"para", "---\nstatus: x", "notes: y\n---"}},
		{"blank second line", "---\n\n# Title\n\nText\n\n---", []string{"---", "# Title", "Text", "---"}},
		{"no closer", "---\nfoo: bar\n\n# H", []string{"---\nfoo: bar", "# H"}},
		{"fence before the closer", "---\nkey: v\n```\n---\n```", []string{"---\nkey: v", "```\n---\n```"}},
		{"prose between dividers", "---\nIntro text\n\n## Heading\n\nBody\n\n---\n\nTail",
			[]string{"---\nIntro text", "## Heading", "Body", "---", "Tail"}},
		{"key without a space after the colon", "---\nhttp://x\n---", []string{"---\nhttp://x\n---"}},
		{"list item", "---\n- a: b\n---", []string{"---", "- a: b", "---"}},
		{"opener with a space", "--- \nfoo: bar\n---", []string{"--- \nfoo: bar\n---"}},
		{"closer with a space", "---\nfoo: bar\n--- ", []string{"---\nfoo: bar\n--- "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Split(tc.doc))
			assert.Equal(t, splitFragment(tc.doc), Split(tc.doc))
			parsed, _, err := parseContent(tc.doc)
			require.NoError(t, err)
			for _, p := range parsed {
				assert.NotEqual(t, editor.TypeFrontMatter, p.Type)
			}
		})
	}
	parsed, _, err := parseContent("---\n\n# Title\n\nText\n\n---")
	require.NoError(t, err)
	types := []string{}
	for _, p := range parsed {
		types = append(types, p.Type)
	}
	assert.Equal(t, []string{editor.TypeDivider, editor.TypeHeading, editor.TypeParagraph, editor.TypeDivider}, types)
}

// Markdown between two dividers stays markdown when a line of it is not
// YAML-shaped, and so does YAML-shaped text over the per-block cap.
func TestFrontMatter_MarkdownStaysMarkdown(t *testing.T) {
	prose := strings.Repeat("A paragraph of prose that goes on for a while.\n\n", 1700)
	keys := strings.Repeat("key: a value that fills the block\n\n", 2000)
	for _, doc := range []string{
		"---\n# Title\n\nText\n\n---\n\nTail",
		"---\n# generated\nk: v\n---",
		"---\n* item: one\n* item: two\n---",
		"---\n1. Step one: do x\n---",
		"---\n> Quote: x\n---",
		"---\n| a: b |\n---",
		"---\nNote: this matters\n\n## Heading\n\nBody\n\n---",
		"---\nNote: intro\n\n" + prose + "---\n\nTail",
		"---\n" + keys + "---\n\nTail",
	} {
		assert.Equal(t, splitFragment(doc), Split(doc), "%.60q", doc)
		parsed, _, err := parseContent(doc)
		require.NoError(t, err, "%.60q", doc)
		for _, p := range parsed {
			assert.NotEqual(t, editor.TypeFrontMatter, p.Type, "%.60q", doc)
		}
	}
}

func TestFrontMatter_FragmentHasNone(t *testing.T) {
	assert.Equal(t, []string{"---\na: 1\n---\nbody"}, splitFragment("---\na: 1\n---\nbody"))
	parsed, _, err := parseBlocks(splitFragment("---\na: 1\n---"), false)
	require.NoError(t, err)
	assert.Equal(t, editor.TypeParagraph, parsed[0].Type)
}

// A leading divider is followed by the blank separator line, so it never
// opens front matter; a document of dividers re-PUTs unchanged.
func TestFrontMatter_LeadingDividerStays(t *testing.T) {
	doc, ok, _ := rePut(t, []existingBlock{
		{Type: editor.TypeDivider},
		{Type: editor.TypeParagraph, Text: "para"},
		{Type: editor.TypeDivider},
	})
	assert.True(t, ok)
	assert.Equal(t, "---\n\npara\n\n---", doc)
}

// A first block that opens like front matter without being it: the lone
// `---` block that would close it renders as `***`; a fence line or a
// line that is not YAML-shaped already ends the scan.
func TestFrontMatter_DividersNeverCloseAFirstParagraph(t *testing.T) {
	doc, ok, _ := rePut(t, []existingBlock{
		{Type: editor.TypeParagraph, Text: "---\nkey: v"},
		{Type: editor.TypeParagraph, Text: "y: 1"},
		{Type: editor.TypeDivider},
		{Type: editor.TypeParagraph, Text: "---"},
	})
	assert.True(t, ok)
	assert.Equal(t, "---\nkey: v\n\ny: 1\n\n***\n\n---", doc)

	for _, tail := range [][]existingBlock{
		{{Type: editor.TypeCode, Text: "---"}, {Type: editor.TypeDivider}},
		{{Type: editor.TypeParagraph, Text: "x"}, {Type: editor.TypeDivider}},
	} {
		stored := append([]existingBlock{{Type: editor.TypeParagraph, Text: "---\nkey: v"}}, tail...)
		doc, ok, _ = rePut(t, stored)
		assert.True(t, ok)
		assert.True(t, strings.HasSuffix(doc, "\n\n---"), "%q keeps its divider", doc)
	}

	doc, ok, _ = rePut(t, []existingBlock{
		{Type: editor.TypeParagraph, Text: "---\nfoo"},
		{Type: editor.TypeDivider},
	})
	assert.True(t, ok)
	assert.Equal(t, "---\nfoo\n\n---", doc)
}

// Blocks the markdown cannot tell apart from what it parses — an image,
// a client-defined type, a paragraph that reads as a list item — stay as
// stored on a re-save of the GET and on an edit elsewhere.
func TestFrontMatter_KeptBlocksKeepTheirType(t *testing.T) {
	stored := []existingBlock{
		{Id: "img", Type: editor.TypeImage, Text: "![a](u)"},
		{Id: "callout", Type: "callout", Text: "note"},
		{Id: "li", Type: editor.TypeParagraph, Text: "- x"},
		{Id: "p", Type: editor.TypeParagraph, Text: "edit me"},
	}
	doc, ok, after := rePut(t, stored)
	assert.True(t, ok)
	assert.Equal(t, stored, after)

	edited, _, _ := save(t, stored, strings.Replace(doc, "edit me", "edited", 1))
	assert.Equal(t, stored[:3], edited[:3])
	assert.Equal(t, "edited", edited[3].Text)
}

// Front matter that cannot open the document as itself renders as the
// yaml code it holds; the next whole-document save of that GET turns it
// into that code block, with the same document.
func TestFrontMatter_ElsewhereRendersAsYAMLCode(t *testing.T) {
	stored := []existingBlock{
		{Type: editor.TypeParagraph},
		{Type: editor.TypeFrontMatter, Text: "a: 1\n\nb: 2"},
	}
	doc, ok, after := rePut(t, stored)
	assert.False(t, ok)
	assert.Equal(t, "\n```yaml\na: 1\n\nb: 2\n```", doc)
	assert.Equal(t, editor.TypeCode, after[1].Type)
	again, ok, _ := rePut(t, after)
	assert.True(t, ok)
	assert.Equal(t, doc, again)

	for _, text := range []string{"\nleading blank", "a: 1\n---\nb: 2", "a: 1\n```", "a: 1\n...\r", "a: 1\r", "plain prose", "# comment\na: 1"} {
		doc, _, after := rePut(t, []existingBlock{{Type: editor.TypeFrontMatter, Text: text}})
		assert.True(t, strings.HasPrefix(doc, "```"), "%q renders as code: %q", text, doc)
		assert.Equal(t, editor.TypeCode, after[0].Type, text)
	}
}

// Saving over stored front matter, or over a first block that opened
// like it: the saved document is the next GET, and dividers keep their
// ids.
func TestFrontMatter_SavedDocumentIsTheNextGet(t *testing.T) {
	cases := []struct {
		name    string
		stored  []existingBlock
		content string
	}{
		{"front matter pushed below the first block",
			[]existingBlock{{Id: "fm", Type: editor.TypeFrontMatter, Text: "a: 1"}}, "intro\n\n---\na: 1\n---"},
		{"front matter below a leading blank line",
			[]existingBlock{{Id: "fm", Type: editor.TypeFrontMatter, Text: "a: 1"}}, "\n---\na: 1\n---"},
		{"front matter saved as yaml code",
			[]existingBlock{{Id: "p", Type: editor.TypeParagraph, Text: "intro"}, {Id: "fm", Type: editor.TypeFrontMatter, Text: "a: 1"}}, "```yaml\na: 1\n```"},
		{"first block no longer opens like front matter",
			[]existingBlock{
				{Id: "p0", Type: editor.TypeParagraph, Text: "---\nkey: v"}, {Id: "x", Type: editor.TypeParagraph, Text: "x"},
				{Id: "d1", Type: editor.TypeDivider}, {Id: "y", Type: editor.TypeParagraph, Text: "y"}, {Id: "d2", Type: editor.TypeDivider},
			}, "bar\n\nx\n\n---\n\ny\n\n---"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored, saved, _ := save(t, tc.stored, tc.content)
			assert.Equal(t, saved, existingDocument(stored))
			for _, b := range stored {
				if b.Type == editor.TypeDivider {
					assert.NotEmpty(t, b.Id, "a divider is kept, not recreated")
				}
			}
		})
	}
}

// A page whose first block opens with `---` without being front matter
// reads back as front matter once its GET is YAML-shaped up to a closer:
// `* x` renders as `- x`. Re-saving that GET converts once, then is stable.
func TestFrontMatter_ResidueConvertsOnce(t *testing.T) {
	stored, saved, _ := save(t, nil, "---\na: b\n* x\n\nc: d\n...\n")
	var types []string
	for _, b := range stored {
		types = append(types, b.Type)
	}
	assert.Equal(t, []string{editor.TypeParagraph, editor.TypeListItem, editor.TypeParagraph}, types)
	doc := existingDocument(stored)
	assert.Equal(t, saved, doc)
	assert.Equal(t, "---\na: b\n\n- x\n\nc: d\n...", doc)

	converted, again, noop := save(t, stored, doc)
	assert.False(t, noop)
	require.Len(t, converted, 1)
	assert.Equal(t, editor.TypeFrontMatter, converted[0].Type)
	assert.Equal(t, again, existingDocument(converted))
	final, ok, _ := rePut(t, converted)
	assert.True(t, ok)
	assert.Equal(t, again, final)
}
