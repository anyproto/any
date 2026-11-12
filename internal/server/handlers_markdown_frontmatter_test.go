package server

import (
	"slices"
	"testing"
)

// TestServer_MarkdownFrontMatter: front matter at the start of a page is
// one front_matter block that comes back byte-for-byte, and re-saving
// the read writes nothing.
func TestServer_MarkdownFrontMatter(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/editor_blocks/markdown"

	doc := "---\nstatus: shipped\nlast-verified: 2026-10-03 (`any`)\n\nnotes: Owner-requested block chrome, …\n---\n\n# PR #145 — Optional editor block handle"
	resp := markdownSet(t, e, md, doc)
	if len(resp.Inserted) != 2 {
		t.Fatalf("set: inserted %v, want 2 blocks", resp.Inserted)
	}
	if got := getMarkdown(t, e, md); got != doc {
		t.Fatalf("get: %q, want %q", got, doc)
	}
	blocks := blocksList(t, e, base)
	if len(blocks.Records) != 2 {
		t.Fatalf("blocks: %d, want 2", len(blocks.Records))
	}
	if fm := blocks.Records[0]; fm.Type != "front_matter" ||
		fm.Text != "status: shipped\nlast-verified: 2026-10-03 (`any`)\n\nnotes: Owner-requested block chrome, …" {
		t.Fatalf("first block: type=%q text=%q", fm.Type, fm.Text)
	}
	again := markdownSet(t, e, md, doc)
	if again.Unchanged != 2 || len(again.Inserted)+len(again.Updated)+len(again.Deleted) != 0 {
		t.Fatalf("resave: %+v, want unchanged=2 and no writes", again)
	}
}

// TestServer_MarkdownFrontMatterStaysPut: a page of dividers re-saves
// writing nothing; front matter below the first block reads as yaml code,
// which the next save of that read stores, with the same document.
func TestServer_MarkdownFrontMatterStaysPut(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/editor_blocks/markdown"

	markdownSet(t, e, md, "---\n\npara\n\n---")
	if types := blockTypes(blocksList(t, e, base)); !slices.Equal(types, []string{"divider", "paragraph", "divider"}) {
		t.Fatalf("dividers: %v", types)
	}
	if again := markdownSet(t, e, md, getMarkdown(t, e, md)); again.Unchanged != 3 || len(again.Inserted)+len(again.Updated)+len(again.Deleted) != 0 {
		t.Fatalf("dividers resave: %+v", again)
	}

	spaceId, objectId = setupBlocksFixture(t, e)
	base = "/v1/spaces/" + spaceId + "/objects/" + objectId
	md = base + "/editor/editor_blocks/markdown"
	blocksCreate(t, e, base, `{"type":"paragraph","text":""}`)
	blocksCreate(t, e, base, `{"type":"front_matter","text":"a: 1\n\nb: 2"}`)
	read := getMarkdownDoc(t, e, md)
	if read.Content != "\n```yaml\na: 1\n\nb: 2\n```" {
		t.Fatalf("front matter below the first block: %q", read.Content)
	}
	saved := markdownPut(t, e, md, read.Content, read.Version)
	if next := getMarkdownDoc(t, e, md); next.Content != read.Content || next.Version != saved.Version {
		t.Fatalf("after the save: %+v, saved version %s", next, saved.Version)
	}
	if types := blockTypes(blocksList(t, e, base)); !slices.Equal(types, []string{"paragraph", "code"}) {
		t.Fatalf("blocks after resave: %v", types)
	}
	if again := markdownPut(t, e, md, read.Content, saved.Version); again.Unchanged != 2 || len(again.Inserted)+len(again.Updated)+len(again.Deleted) != 0 {
		t.Fatalf("second resave: %+v", again)
	}
}

// TestServer_MarkdownSavedVersionIsTheNextGet: whatever the stored blocks,
// a PUT's version is the next GET's, so a save chained on it is accepted;
// dividers a first-block change re-renders keep their ids.
func TestServer_MarkdownSavedVersionIsTheNextGet(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	cases := []struct {
		name    string
		seed    []string // blocks API bodies, in order
		content string
	}{
		{"front matter pushed below the first block", []string{`{"type":"front_matter","text":"a: 1"}`}, "intro\n\n---\na: 1\n---"},
		{"front matter below a leading blank line", []string{`{"type":"front_matter","text":"a: 1"}`}, "\n---\na: 1\n---"},
		{"front matter saved as yaml code", []string{`{"type":"paragraph","text":"intro"}`, `{"type":"front_matter","text":"a: 1"}`}, "```yaml\na: 1\n```"},
		{"first block no longer opens like front matter", []string{
			`{"type":"paragraph","text":"---\nfoo"}`, `{"type":"paragraph","text":"x"}`, `{"type":"divider"}`,
			`{"type":"paragraph","text":"y"}`, `{"type":"divider"}`,
		}, "bar\n\nx\n\n---\n\ny\n\n---"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spaceId, objectId := setupBlocksFixture(t, e)
			base := "/v1/spaces/" + spaceId + "/objects/" + objectId
			md := base + "/editor/editor_blocks/markdown"
			var dividers []string
			for _, body := range tc.seed {
				if b := blocksCreate(t, e, base, body); b.Type == "divider" {
					dividers = append(dividers, b.Id)
				}
			}
			saved := markdownPut(t, e, md, tc.content, getMarkdownDoc(t, e, md).Version)
			next := getMarkdownDoc(t, e, md)
			if next.Version != saved.Version || next.Content != tc.content {
				t.Fatalf("next GET %+v, saved version %s", next, saved.Version)
			}
			markdownPut(t, e, md, tc.content+"\n\nmore", saved.Version)
			var kept []string
			for _, b := range blocksList(t, e, base).Records {
				if b.Type == "divider" {
					kept = append(kept, b.Id)
				}
			}
			if len(dividers) > 0 && !slices.Equal(kept, dividers) {
				t.Fatalf("dividers %v, want the seeded %v", kept, dividers)
			}
		})
	}
}

func blockTypes(list blocksListResp) []string {
	out := make([]string, 0, len(list.Records))
	for _, b := range list.Records {
		out = append(out, b.Type)
	}
	return out
}

// TestServer_MarkdownKeepsBlockTypes: an image, a client-defined type and
// a paragraph reading as a list item stay as stored, with no write, on a
// re-save of the GET and on an edit elsewhere in the page.
func TestServer_MarkdownKeepsBlockTypes(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/editor_blocks/markdown"
	blocksCreate(t, e, base, `{"type":"image","text":"![a](u)"}`)
	blocksCreate(t, e, base, `{"type":"callout","text":"note"}`)
	blocksCreate(t, e, base, `{"type":"paragraph","text":"- x"}`)
	blocksCreate(t, e, base, `{"type":"paragraph","text":"edit me"}`)
	want := []string{"image", "callout", "paragraph", "paragraph"}

	if again := markdownSet(t, e, md, getMarkdown(t, e, md)); again.Unchanged != 4 || len(again.Inserted)+len(again.Updated)+len(again.Deleted) != 0 {
		t.Fatalf("resave: %+v", again)
	}
	if types := blockTypes(blocksList(t, e, base)); !slices.Equal(types, want) {
		t.Fatalf("after resave: %v", types)
	}
	edit := markdownEdit(t, e, base, `{"edits":[{"oldText":"edit me","newText":"edited"}]}`)
	if len(edit.Updated) != 1 || len(edit.Inserted)+len(edit.Deleted) != 0 || edit.Unchanged != 3 {
		t.Fatalf("edit: %+v", edit)
	}
	if types := blockTypes(blocksList(t, e, base)); !slices.Equal(types, want) {
		t.Fatalf("after edit: %v", types)
	}
}

// TestServer_MarkdownAppendStartsADocument: content appended to a page
// with no blocks starts the document, front matter included; appended to
// a page with blocks it is a fragment.
func TestServer_MarkdownAppendStartsADocument(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/editor_blocks/markdown"
	doc := "---\ntitle: x\ntags:\n  - a\n---\n\n# Body"
	appendMarkdown(t, e, base, doc)
	if got := getMarkdown(t, e, md); got != doc {
		t.Fatalf("append to an empty page: %q, want %q", got, doc)
	}
	appendMarkdown(t, e, base, "---\na: 1\n---")
	if types := blockTypes(blocksList(t, e, base)); slices.Contains(types[2:], "front_matter") {
		t.Fatalf("a fragment became front matter: %v", types)
	}
}
