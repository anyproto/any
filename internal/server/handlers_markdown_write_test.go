package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/editor"
)

// TestServer_MarkdownKeepsStyleKeysItCannotExpress pins that a markdown
// save owns only the style keys the markdown expresses: a key set
// through …/blocks survives a level change, a text edit and a change of
// block type, and the block keeps its id throughout.
func TestServer_MarkdownKeepsStyleKeysItCannotExpress(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/editor_blocks/markdown"

	only := func(step string) block {
		t.Helper()
		blocks := blocksList(t, e, base).Records
		if len(blocks) != 1 {
			t.Fatalf("%s: %d blocks, want 1", step, len(blocks))
		}
		return blocks[0]
	}

	markdownSet(t, e, md, "plain text here")
	id := only("seed").Id
	markdownSet(t, e, md, "## plain text here")
	if b := only("to heading"); b.Type != "heading" || b.Style["level"] != float64(2) {
		t.Fatalf("to heading: %s %v", b.Type, b.Style)
	}

	rec := doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+id, `{"set":{"style.color":"red"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch style: %d %s", rec.Code, rec.Body.String())
	}

	steps := []struct {
		content, wantType string
		wantLevel         any
	}{
		{"### plain text here", "heading", float64(3)},
		{"### plain text changed", "heading", float64(3)},
		{"plain text changed", "paragraph", nil},
	}
	for _, s := range steps {
		markdownSet(t, e, md, s.content)
		b := only(s.content)
		if b.Id != id {
			t.Fatalf("%q: block id %s, want the original %s", s.content, b.Id, id)
		}
		if b.Type != s.wantType || b.Style["level"] != s.wantLevel {
			t.Errorf("%q: %s level=%v, want %s level=%v", s.content, b.Type, b.Style["level"], s.wantType, s.wantLevel)
		}
		if b.Style["color"] != "red" {
			t.Errorf("%q: style %v lost the color the markdown cannot express", s.content, b.Style)
		}
	}

	// A paragraph's level (indentation set through …/blocks) is not the
	// markdown's to clear: a text edit keeps it.
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+id, `{"set":{"style.level":2}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch paragraph level: %d %s", rec.Code, rec.Body.String())
	}
	markdownSet(t, e, md, "plain text changed again")
	if b := only("paragraph text edit"); b.Id != id || b.Style["level"] != float64(2) || b.Style["color"] != "red" {
		t.Errorf("paragraph text edit: id %s style %v, want id %s with level 2 and the color kept", b.Id, b.Style, id)
	}
}

// TestServer_MarkdownRefusesOversizedBlock pins that a block over the
// editor's per-block cap is refused before anything is written, on
// every markdown write: the editor would otherwise reject it mid-change
// and leave the rest of the save applied.
func TestServer_MarkdownRefusesOversizedBlock(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	md := "/v1/spaces/" + spaceId + "/objects/" + objectId + "/editor/editor_blocks/markdown"
	markdownSet(t, e, md, "first old paragraph here\n\nsecond old paragraph here")
	before := getMarkdownDoc(t, e, md)

	big := strings.Repeat("x", editor.MaxTextBytes+1)
	put, _ := json.Marshal(api.MarkdownSetRequest{Content: "zzz completely new\n\n" + big, IfVersion: before.Version})
	appendBody, _ := json.Marshal(api.MarkdownContent{Content: big})
	patch, _ := json.Marshal(api.MarkdownEditRequest{Edits: []api.MarkdownEdit{{OldText: "second old paragraph here", NewText: big}}})
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPut, md, string(put)},
		{http.MethodPost, md + "/append", string(appendBody)},
		{http.MethodPatch, md, string(patch)},
	} {
		rec := doJSON(t, e, req.method, req.path, req.body)
		var env api.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != http.StatusBadRequest || env.Error.Code != api.ErrMarkdownBlockTooLarge {
			t.Errorf("%s %s: %d %s, want 400 %s", req.method, req.path, rec.Code, env.Error.Code, api.ErrMarkdownBlockTooLarge)
		}
	}
	if after := getMarkdownDoc(t, e, md); after != before {
		t.Fatalf("a refused write changed the document: %+v, want %+v", after, before)
	}
}

// TestServer_MarkdownKeepsNestedBlocksOfReplacedBlocks pins that a
// markdown save deleting a block — here one rewritten past recognition,
// which the diff reads as a delete plus a create, as it reads a move —
// leaves the blocks nested under it live: their content is nothing the
// markdown shows, and the …/blocks routes still reach it.
func TestServer_MarkdownKeepsNestedBlocksOfReplacedBlocks(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/editor_blocks/markdown"

	markdownSet(t, e, md, "alpha\n\nbeta")
	beta := blocksList(t, e, base).Records[1]
	child := blocksCreate(t, e, base, `{"type":"paragraph","text":"child","nav":{"parentId":"`+beta.Id+`"}}`)

	res := markdownSet(t, e, md, "alpha\n\nsomething else entirely, nothing like the old text")
	if len(res.Deleted) != 1 || res.Deleted[0] != beta.Id {
		t.Fatalf("deleted %v, want only the replaced block %s", res.Deleted, beta.Id)
	}
	for _, b := range blocksList(t, e, base).Records {
		if b.Id == child.Id {
			return
		}
	}
	t.Fatalf("the nested block %s under the replaced block was deleted", child.Id)
}
