// Single-binary editor/blocks coverage. internal/server/handlers_blocks_test.go
// already drives the same handlers in-process; what's missing is a smoke
// test that runs the compiled `any` binary, talks JSON over a real TCP
// socket, and walks both the atomic /editor/blocks endpoints and their
// /editor/markdown counterpart through to convergence.
//
// The two paths share the editor_blocks dataset, so the headline contract
// is "a block API write and a markdown PUT produce equivalent end state
// when given equivalent inputs"; both this file and the in-process
// counterpart pin that down.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/editor"
)

// TestE2E_EditorBlocksBinary covers the full block-API CRUD cycle plus
// the markdown import/export bridge through the real binary. Sequence:
//
//  1. Bootstrap space + object.
//  2. List on a fresh object returns records=[] (dataset empty until
//     the first write).
//  3. Markdown GET on an empty object returns content="".
//  4. Create three top-level blocks via POST /editor/blocks; assert
//     server-allocated nav.pos values are strictly ascending and the
//     auto-derived block ids are unique.
//  5. List returns them in document order; each carries a non-empty
//     `_ver`.
//  6. Markdown GET renders the same body the blocks API just wrote.
//  7. PATCH the middle block (set text + style); GET reflects the new
//     value, untouched neighbours unchanged.
//  8. PATCH unset clears the style.
//  9. DELETE the first block; subsequent PATCH/DELETE on its id return
//     404 blocks.not_found.
//  10. Nested tree: create two children under the surviving top-level
//      block; list returns parent-then-children in DFS order.
//  11. Validation envelopes: POST without `type` → 400
//      blocks.type_required.
func TestE2E_EditorBlocksBinary(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"editor-binary"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	objBase := base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	// 2. Fresh object — empty list.
	var initial api.BlockListResponse
	mustJSON(t, http.MethodGet, objBase+"/editor/blocks", "",
		http.StatusOK, &initial)
	if len(initial.Records) != 0 {
		t.Fatalf("initial list = %d records, want 0: %+v", len(initial.Records), initial.Records)
	}

	// 3. Empty object → empty markdown.
	if got := getMarkdownContent(t, objBase+"/editor/markdown"); got != "" {
		t.Errorf("empty markdown = %q, want \"\"", got)
	}

	// 4. Create three top-level blocks of varied shapes.
	first := createBlock(t, objBase, `{"type":"paragraph","text":"alpha"}`)
	second := createBlock(t, objBase, `{"type":"heading","style":{"level":2},"text":"beta"}`)
	third := createBlock(t, objBase, `{"type":"paragraph","text":"gamma"}`)

	ids := []string{first.Id, second.Id, third.Id}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatalf("block id empty in %v", ids)
		}
		if seen[id] {
			t.Fatalf("duplicate block id %q in %v", id, ids)
		}
		seen[id] = true
	}
	if !(first.Nav.Pos < second.Nav.Pos && second.Nav.Pos < third.Nav.Pos) {
		t.Errorf("nav.pos not ascending: %q < %q < %q",
			first.Nav.Pos, second.Nav.Pos, third.Nav.Pos)
	}
	if got := second.Style["level"]; toInt(got) != 2 {
		t.Errorf("second.style.level = %v, want 2", got)
	}

	// 5. List in document order with _ver populated.
	var listed api.BlockListResponse
	mustJSON(t, http.MethodGet, objBase+"/editor/blocks", "",
		http.StatusOK, &listed)
	if len(listed.Records) != 3 {
		t.Fatalf("list = %d, want 3", len(listed.Records))
	}
	for i, b := range listed.Records {
		if b.Id != ids[i] {
			t.Errorf("listed[%d].Id = %s, want %s", i, b.Id, ids[i])
		}
		if len(b.Ver) == 0 {
			t.Errorf("listed[%d]._ver missing", i)
		}
	}

	// 6. Markdown GET reflects the blocks-API writes.
	wantMd := "alpha\n\n## beta\n\ngamma"
	if got := getMarkdownContent(t, objBase+"/editor/markdown"); got != wantMd {
		t.Errorf("markdown after blocks-API writes:\nwant: %q\ngot:  %q", wantMd, got)
	}

	// 7. Patch the middle block — set text + add style entry. Confirms
	// atomic multi-field $set in one PATCH.
	patchResp := patchBlock(t, objBase, second.Id,
		`{"set":{"text":"beta-edited","style":{"level":3}}}`)
	if patchResp.VersionId == "" {
		t.Errorf("patch versionId empty")
	}

	listed = listBlocks(t, objBase)
	if listed.Records[1].Text != "beta-edited" {
		t.Errorf("post-patch text = %q, want beta-edited", listed.Records[1].Text)
	}
	if lvl := toInt(listed.Records[1].Style["level"]); lvl != 3 {
		t.Errorf("post-patch style.level = %v, want 3", lvl)
	}
	if listed.Records[0].Text != "alpha" || listed.Records[2].Text != "gamma" {
		t.Errorf("neighbouring blocks clobbered: %+v", listed.Records)
	}

	// 8. Unset clears style.
	patchBlock(t, objBase, second.Id, `{"unset":["style"]}`)
	listed = listBlocks(t, objBase)
	if listed.Records[1].Style != nil {
		t.Errorf("post-unset style = %+v, want nil", listed.Records[1].Style)
	}

	// 9. Delete first; subsequent ops on its id return 404
	// blocks.not_found.
	mustStatus(t, http.MethodDelete, objBase+"/editor/blocks/"+first.Id, "",
		http.StatusNoContent)

	listed = listBlocks(t, objBase)
	if len(listed.Records) != 2 {
		t.Fatalf("post-delete list = %d, want 2", len(listed.Records))
	}
	for _, b := range listed.Records {
		if b.Id == first.Id {
			t.Errorf("deleted block %s still in list", first.Id)
		}
	}

	// PATCH on a tombstoned block surfaces 404 (Patch reads the record
	// first to keep edits author-checkable). DELETE is idempotent at
	// the SDK layer — the second delete returns 204 with no effect.
	var env api.ErrorEnvelope
	mustJSON(t, http.MethodPatch, objBase+"/editor/blocks/"+first.Id,
		`{"set":{"text":"resurrect"}}`, http.StatusNotFound, &env)
	if env.Error.Code != api.ErrBlockNotFound {
		t.Errorf("PATCH after delete: code = %q, want %q", env.Error.Code, api.ErrBlockNotFound)
	}
	mustStatus(t, http.MethodDelete, objBase+"/editor/blocks/"+first.Id, "",
		http.StatusNoContent)

	// 10. Nested tree: create two children under `second` (which
	// survives the delete). DFS order = [..., second, childA, childB,
	// third] with `third` still at the end because its pos is past
	// second's children.
	childA := createBlock(t, objBase,
		fmt.Sprintf(`{"type":"paragraph","text":"child-a","nav":{"parentId":%q}}`, second.Id))
	childB := createBlock(t, objBase,
		fmt.Sprintf(`{"type":"paragraph","text":"child-b","nav":{"parentId":%q}}`, second.Id))

	listed = listBlocks(t, objBase)
	if len(listed.Records) != 4 {
		t.Fatalf("nested list = %d, want 4 (second, childA, childB, third); got=%+v",
			len(listed.Records), listed.Records)
	}
	wantOrder := []string{second.Id, childA.Id, childB.Id, third.Id}
	for i, want := range wantOrder {
		if listed.Records[i].Id != want {
			t.Errorf("DFS[%d] = %s, want %s (full=%+v)",
				i, listed.Records[i].Id, want, listed.Records)
		}
	}

	// 11. Validation: POST without `type` → 400 blocks.type_required.
	mustJSON(t, http.MethodPost, objBase+"/editor/blocks",
		`{"text":"missing type"}`, http.StatusBadRequest, &env)
	if env.Error.Code != api.ErrBlockTypeMissing {
		t.Errorf("missing type: code = %q, want %q", env.Error.Code, api.ErrBlockTypeMissing)
	}
}

// TestE2E_EditorMarkdownRoundTrip covers the markdown import/export
// surface against the binary. Sequence:
//
//  1. PUT a multi-block document → response carries inserted=[…] with
//     one id per top-level block; updated/deleted empty.
//  2. GET returns identical bytes (round-trip canonical).
//  3. PUT a modified document — one updated, one deleted, one
//     inserted, one unchanged → diff stats reflect that mix.
//  4. PUT "" empties the dataset.
//  5. Path-segment safety: the SDK suffixes batch-derived ids with
//     `:N`; PATCH and DELETE on a suffixed id round-trip through
//     echo's :blockId param without escaping.
func TestE2E_EditorMarkdownRoundTrip(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"md-roundtrip"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	objBase := base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId
	mdURL := objBase + "/editor/markdown"

	// 1. Initial PUT — three top-level blocks of three different types.
	// Use the canonical inline-only shapes the renderer emits so the
	// GET round-trip below is byte-identical.
	original := "# Title\n\nfirst paragraph\n\n## Subhead\n\nsecond paragraph\n\n- list item one\n\n- list item two"
	setResp := putMarkdown(t, mdURL, original)
	if len(setResp.Inserted) != 6 {
		t.Fatalf("initial PUT inserted = %d, want 6 (heading, para, heading, para, li, li); resp=%+v",
			len(setResp.Inserted), setResp)
	}
	if len(setResp.Updated) != 0 || len(setResp.Deleted) != 0 {
		t.Errorf("initial PUT had unexpected updated/deleted: %+v", setResp)
	}
	if setResp.Unchanged != 0 {
		t.Errorf("initial PUT unchanged = %d, want 0", setResp.Unchanged)
	}
	for i, id := range setResp.Inserted {
		if id == "" {
			t.Errorf("inserted[%d] id empty", i)
		}
	}

	// 2. GET round-trip.
	if got := getMarkdownContent(t, mdURL); got != original {
		t.Errorf("markdown round-trip mismatch\nwant: %q\ngot:  %q", original, got)
	}

	// Confirm the blocks dataset agrees with the markdown view.
	listed := listBlocks(t, objBase)
	if len(listed.Records) != 6 {
		t.Fatalf("blocks list after PUT = %d, want 6", len(listed.Records))
	}
	// Spot-check types — the markdown layer recognised them correctly.
	wantTypes := []string{
		editor.TypeHeading, editor.TypeParagraph,
		editor.TypeHeading, editor.TypeParagraph,
		editor.TypeListItem, editor.TypeListItem,
	}
	for i, b := range listed.Records {
		if b.Type != wantTypes[i] {
			t.Errorf("listed[%d].Type = %q, want %q", i, b.Type, wantTypes[i])
		}
	}

	// 3. Modified document: keep "# Title", change "first paragraph",
	// drop "## Subhead" + its body and both list items, insert two new
	// paragraphs.
	modified := "# Title\n\nfirst paragraph edited\n\nbrand new line\n\nanother fresh line"
	modResp := putMarkdown(t, mdURL, modified)
	// At least one updated and one deleted entry, plus inserted for the
	// new tail lines. We don't pin exact counts because the diff
	// algorithm can choose either "update body + delete the rest" or
	// "update body + update head + delete tail"; both are valid plans.
	if modResp.Unchanged == 0 {
		t.Errorf("modified PUT: expected at least one unchanged (\"# Title\"), got %+v", modResp)
	}
	if len(modResp.Inserted)+len(modResp.Updated) == 0 {
		t.Errorf("modified PUT: nothing inserted+updated: %+v", modResp)
	}
	if len(modResp.Deleted) == 0 {
		t.Errorf("modified PUT: nothing deleted (subhead + list items should be gone): %+v", modResp)
	}

	// GET reflects the modification.
	if got := getMarkdownContent(t, mdURL); got != modified {
		t.Errorf("markdown after modify\nwant: %q\ngot:  %q", modified, got)
	}

	// 4. Empty PUT tombstones every block.
	emptyResp := putMarkdown(t, mdURL, "")
	if len(emptyResp.Deleted) == 0 {
		t.Errorf("empty PUT: nothing deleted, blocks should all be gone: %+v", emptyResp)
	}
	if len(emptyResp.Inserted) != 0 || len(emptyResp.Updated) != 0 {
		t.Errorf("empty PUT: spurious inserted/updated: %+v", emptyResp)
	}
	if got := getMarkdownContent(t, mdURL); got != "" {
		t.Errorf("post-empty markdown = %q, want \"\"", got)
	}
	listed = listBlocks(t, objBase)
	if len(listed.Records) != 0 {
		t.Errorf("post-empty list = %d records, want 0; first=%+v", len(listed.Records), listed.Records[0])
	}

	// 5. Path-segment safety. Batched markdown PUT produces ids with a
	// `:N` suffix; the standard :blockId route param must accept them
	// without escaping. Three blocks → two suffixed ids on the same
	// ChangeId base.
	batched := "first batched\n\nsecond batched\n\nthird batched"
	batchResp := putMarkdown(t, mdURL, batched)
	if len(batchResp.Inserted) != 3 {
		t.Fatalf("batched PUT inserted = %d, want 3", len(batchResp.Inserted))
	}
	listed = listBlocks(t, objBase)
	var suffixed string
	for _, b := range listed.Records {
		if strings.Contains(b.Id, ":") {
			suffixed = b.Id
			break
		}
		if strings.ContainsAny(b.Id, "/") {
			t.Fatalf("block id %q contains `/` — separator regressed; routes will 404", b.Id)
		}
	}
	if suffixed == "" {
		ids := make([]string, 0, len(listed.Records))
		for _, b := range listed.Records {
			ids = append(ids, b.Id)
		}
		t.Fatalf("no `:`-suffixed id in batched PUT result: %v", ids)
	}

	patchBlock(t, objBase, suffixed, `{"set":{"text":"patched batched"}}`)
	mustStatus(t, http.MethodDelete, objBase+"/editor/blocks/"+suffixed, "",
		http.StatusNoContent)

	listed = listBlocks(t, objBase)
	for _, b := range listed.Records {
		if b.Id == suffixed {
			t.Errorf("suffixed block %s not tombstoned", suffixed)
		}
	}
}

// TestE2E_EditorBlocksMarkdownConvergence pins down the headline
// contract: a sequence of POST /editor/blocks calls and a single PUT
// /editor/markdown produce equivalent final state when given equivalent
// inputs. Two objects in the same space, two write strategies, one
// markdown bytes string for comparison.
func TestE2E_EditorBlocksMarkdownConvergence(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"converge"}`, http.StatusCreated, &sp)

	var viaMd, viaBlocks api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &viaMd)
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &viaBlocks)

	mdObjBase := base + "/v1/spaces/" + sp.Id + "/objects/" + viaMd.ObjectId
	blObjBase := base + "/v1/spaces/" + sp.Id + "/objects/" + viaBlocks.ObjectId

	doc := "# Doc\n\nfirst paragraph\n\n## Section\n\nsecond paragraph"

	// Path A: PUT markdown.
	putMarkdown(t, mdObjBase+"/editor/markdown", doc)

	// Path B: equivalent series of POST /editor/blocks. Mirrors the
	// per-block shapes the markdown parser produces.
	createBlock(t, blObjBase, `{"type":"heading","style":{"level":1},"text":"Doc"}`)
	createBlock(t, blObjBase, `{"type":"paragraph","text":"first paragraph"}`)
	createBlock(t, blObjBase, `{"type":"heading","style":{"level":2},"text":"Section"}`)
	createBlock(t, blObjBase, `{"type":"paragraph","text":"second paragraph"}`)

	gotMd := getMarkdownContent(t, mdObjBase+"/editor/markdown")
	gotBl := getMarkdownContent(t, blObjBase+"/editor/markdown")
	if gotMd != gotBl {
		t.Errorf("rendered markdown diverged across write paths\nvia markdown PUT:\n%s\nvia blocks API:\n%s",
			gotMd, gotBl)
	}
	if gotMd != doc {
		t.Errorf("markdown round-trip lost bytes\nwant: %q\ngot:  %q", doc, gotMd)
	}

	// Block trees should also agree on the shape (type, text, ordering)
	// even though the auto-derived ids differ between objects.
	mdList := listBlocks(t, mdObjBase)
	blList := listBlocks(t, blObjBase)
	if len(mdList.Records) != len(blList.Records) {
		t.Fatalf("block count: markdown PUT=%d, blocks API=%d", len(mdList.Records), len(blList.Records))
	}
	for i := range mdList.Records {
		m, b := mdList.Records[i], blList.Records[i]
		if m.Type != b.Type {
			t.Errorf("[%d] type: md=%q, bl=%q", i, m.Type, b.Type)
		}
		if m.Text != b.Text {
			t.Errorf("[%d] text: md=%q, bl=%q", i, m.Text, b.Text)
		}
		if toInt(m.Style["level"]) != toInt(b.Style["level"]) {
			t.Errorf("[%d] style.level: md=%v, bl=%v", i, m.Style["level"], b.Style["level"])
		}
	}
}

// TestE2E_EditorBlocksSSE opens an SSE stream against dataset=editor_blocks
// and asserts a create + a markdown-PUT-driven update + a delete each
// surface as a `changes` frame against the running binary. Confirms
// both the atomic /editor/blocks endpoints and /editor/markdown share
// the same event firehose.
func TestE2E_EditorBlocksSSE(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr
	cl := client.New(addr, 0)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"blocks-sse"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	objBase := base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	frames := make(chan client.SSEFrame, 32)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- cl.StreamSubscribeObject(streamCtx, sp.Id, obj.ObjectId, editor.Dataset,
			func(f client.SSEFrame) error {
				select {
				case frames <- f:
				case <-streamCtx.Done():
				}
				return nil
			})
	}()

	if first := waitFrameWithTimeout(t, frames, 10*time.Second); first.Event != "ready" {
		t.Fatalf("first frame = %q, want ready (data=%s)", first.Event, first.Data)
	}

	// 1. POST /editor/blocks fires a `changes` frame with deleted=false.
	created := createBlock(t, objBase, `{"type":"paragraph","text":"sse-1"}`)
	createEvt := awaitEditorBlocksEvent(t, frames, created.Id)
	if createEvt.VersionId == "" {
		t.Errorf("create event missing versionId")
	}
	if len(createEvt.Records) == 0 || createEvt.Records[0].Deleted {
		t.Errorf("create event = %+v, want non-deleted record for %s", createEvt, created.Id)
	}

	// 2. PUT /editor/markdown drives the same dataset — a follow-up
	// modification of the same block must fire a fresh changes frame.
	// The renderer emits one paragraph per top-level block; the existing
	// record's text canonical form is "sse-1", so we re-PUT with that
	// plus a second block to force an insert event.
	putMarkdown(t, objBase+"/editor/markdown", "sse-1\n\nsse-2-new")

	// We expect at least one more event referencing a editor_blocks
	// record — either an insert for the new line, or an update on the
	// existing block depending on diff alignment. Wait for any one.
	if !awaitAnyEditorBlocksEvent(t, frames, 10*time.Second) {
		t.Fatalf("no editor_blocks event after markdown PUT")
	}

	// 3. DELETE /editor/blocks fires a deleted=true event.
	mustStatus(t, http.MethodDelete, objBase+"/editor/blocks/"+created.Id, "",
		http.StatusNoContent)
	deleteEvt := awaitEditorBlocksEvent(t, frames, created.Id)
	if len(deleteEvt.Records) == 0 || !deleteEvt.Records[0].Deleted {
		t.Errorf("delete event = %+v, want deleted=true on %s", deleteEvt, created.Id)
	}

	streamCancel()
	select {
	case <-streamErr:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not exit after cancel")
	}
}

// --- helpers ---------------------------------------------------------------

func createBlock(t *testing.T, objBase, body string) api.Block {
	t.Helper()
	var b api.Block
	mustJSON(t, http.MethodPost, objBase+"/editor/blocks", body,
		http.StatusCreated, &b)
	return b
}

func patchBlock(t *testing.T, objBase, blockId, body string) api.BlockPatchResponse {
	t.Helper()
	var resp api.BlockPatchResponse
	mustJSON(t, http.MethodPatch, objBase+"/editor/blocks/"+blockId, body,
		http.StatusOK, &resp)
	return resp
}

func listBlocks(t *testing.T, objBase string) api.BlockListResponse {
	t.Helper()
	var resp api.BlockListResponse
	mustJSON(t, http.MethodGet, objBase+"/editor/blocks", "",
		http.StatusOK, &resp)
	return resp
}

func getMarkdownContent(t *testing.T, mdURL string) string {
	t.Helper()
	var resp struct {
		Content string `json:"content"`
	}
	mustJSON(t, http.MethodGet, mdURL, "", http.StatusOK, &resp)
	return resp.Content
}

// markdownSetResponse mirrors the wire shape of PUT /editor/markdown.
// Re-declared here (rather than imported from internal/api) because the
// handler ships the response as an inline map without a Go type — same
// approach the in-process tests take.
type markdownSetResponse struct {
	Inserted  []string `json:"inserted"`
	Updated   []string `json:"updated"`
	Deleted   []string `json:"deleted"`
	Unchanged int      `json:"unchanged"`
}

func putMarkdown(t *testing.T, mdURL, content string) markdownSetResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	var resp markdownSetResponse
	mustJSON(t, http.MethodPut, mdURL, string(body), http.StatusOK, &resp)
	return resp
}

// toInt squashes the numeric variants the JSON decoder might leave on
// style values (int / int64 / float64) into a Go int for assertions.
// Returns 0 on any non-numeric.
func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

// awaitEditorBlocksEvent pulls SSE frames until it finds a `changes` frame
// whose batch contains an event for (editor_blocks, recordId). Other
// frames are discarded. Times out after 10s.
func awaitEditorBlocksEvent(t *testing.T, frames <-chan client.SSEFrame, recordId string) api.SubscribeEvent {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-frames:
			if f.Event != "changes" {
				continue
			}
			var batch []api.SubscribeEvent
			if err := json.Unmarshal(f.Data, &batch); err != nil {
				t.Fatalf("decode changes batch: %v\nraw=%s", err, f.Data)
			}
			for _, ev := range batch {
				if ev.Dataset != editor.Dataset {
					continue
				}
				for _, rec := range ev.Records {
					if rec.Id == recordId {
						return ev
					}
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for editor_blocks event on %s", recordId)
			return api.SubscribeEvent{}
		}
	}
}

// awaitAnyEditorBlocksEvent waits for *any* changes frame on the
// editor_blocks dataset (records list ignored). Used when the test driver
// can't predict which specific record id will fire — markdown PUT might
// emit either an Update on the existing record or an Insert on a new
// one depending on how the diff aligns.
func awaitAnyEditorBlocksEvent(t *testing.T, frames <-chan client.SSEFrame, d time.Duration) bool {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f := <-frames:
			if f.Event != "changes" {
				continue
			}
			var batch []api.SubscribeEvent
			if err := json.Unmarshal(f.Data, &batch); err != nil {
				t.Fatalf("decode changes batch: %v", err)
			}
			for _, ev := range batch {
				if ev.Dataset == editor.Dataset {
					return true
				}
			}
		case <-deadline:
			return false
		}
	}
}
