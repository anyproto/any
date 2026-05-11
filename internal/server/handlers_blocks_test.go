package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/editor"
	"github.com/anyproto/any/internal/client"
)

// TestServer_Blocks_RoundTrip drives the create / list / patch /
// delete cycle through echo against a real SDK. Verifies server-
// allocated nav.pos, _ver presence, atomic patch semantics, and
// tombstone behaviour.
func TestServer_Blocks_RoundTrip(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// 1. Initially empty.
	rec := doJSON(t, e, http.MethodGet, base+"/editor/blocks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("initial list: %d %s", rec.Code, rec.Body.String())
	}
	var initial api.BlockListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode initial list: %v", err)
	}
	if len(initial.Records) != 0 {
		t.Fatalf("expected empty list, got %d records", len(initial.Records))
	}

	// 2. Create two top-level blocks.
	first := blocksCreate(t, e, base, `{"type":"paragraph","text":"first"}`)
	second := blocksCreate(t, e, base, `{"type":"heading","style":{"level":2},"text":"second"}`)
	if first.Id == "" || second.Id == "" {
		t.Fatalf("ids empty: first=%+v second=%+v", first, second)
	}
	if first.Nav.Pos == "" || second.Nav.Pos == "" {
		t.Errorf("nav.pos unset: first=%q second=%q", first.Nav.Pos, second.Nav.Pos)
	}
	if !(first.Nav.Pos < second.Nav.Pos) {
		t.Errorf("expected ascending pos, got %q then %q", first.Nav.Pos, second.Nav.Pos)
	}

	// 3. List returns both in document order.
	listed := blocksList(t, e, base)
	if len(listed.Records) != 2 {
		t.Fatalf("list returned %d, want 2", len(listed.Records))
	}
	if listed.Records[0].Id != first.Id || listed.Records[1].Id != second.Id {
		t.Errorf("order: got [%s %s], want [%s %s]",
			listed.Records[0].Id, listed.Records[1].Id, first.Id, second.Id)
	}
	for _, b := range listed.Records {
		if len(b.Ver) == 0 {
			t.Errorf("missing _ver on %s", b.Id)
		}
	}

	// 4. Patch the first block — change text and add a style entry.
	patchBody := `{"set":{"text":"first edited","style":{"emphasis":true}}}`
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/blocks/"+first.Id, patchBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	var patchResp api.BlockPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &patchResp); err != nil {
		t.Fatalf("decode patch resp: %v", err)
	}
	if patchResp.VersionId == "" {
		t.Errorf("patch versionId empty")
	}

	// Re-list and confirm only the patched block changed.
	listed = blocksList(t, e, base)
	if listed.Records[0].Text != "first edited" {
		t.Errorf("post-patch text = %q, want %q", listed.Records[0].Text, "first edited")
	}
	if listed.Records[1].Text != "second" {
		t.Errorf("untouched block text = %q, want %q", listed.Records[1].Text, "second")
	}

	// 5. Unset the style.
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/blocks/"+first.Id,
		`{"unset":["style"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH unset: %d %s", rec.Code, rec.Body.String())
	}
	listed = blocksList(t, e, base)
	if listed.Records[0].Style != nil {
		t.Errorf("post-unset style should be nil, got %+v", listed.Records[0].Style)
	}

	// 6. Delete the second block.
	rec = doJSON(t, e, http.MethodDelete, base+"/editor/blocks/"+second.Id, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}

	listed = blocksList(t, e, base)
	if len(listed.Records) != 1 {
		t.Fatalf("post-delete list: %d records, want 1", len(listed.Records))
	}
	if listed.Records[0].Id != first.Id {
		t.Errorf("post-delete remaining id = %s, want %s", listed.Records[0].Id, first.Id)
	}

	// 7. Patching a deleted block returns 404.
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/blocks/"+second.Id, `{"set":{"text":"resurrect"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH deleted: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// TestServer_Blocks_NestedTree confirms parentId/pos navigation: a
// block with parentId=<other.Id> appears after its parent in the
// flat list, and listing returns DFS document order.
func TestServer_Blocks_NestedTree(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	parent := blocksCreate(t, e, base, `{"type":"list_item","style":{"ordered":false},"text":"parent"}`)
	childA := blocksCreate(t, e, base,
		fmt.Sprintf(`{"type":"paragraph","text":"child a","nav":{"parentId":%q}}`, parent.Id))
	childB := blocksCreate(t, e, base,
		fmt.Sprintf(`{"type":"paragraph","text":"child b","nav":{"parentId":%q}}`, parent.Id))

	listed := blocksList(t, e, base)
	if len(listed.Records) != 3 {
		t.Fatalf("list: %d records, want 3", len(listed.Records))
	}
	// DFS: parent, childA, childB.
	wantOrder := []string{parent.Id, childA.Id, childB.Id}
	for i, b := range listed.Records {
		if b.Id != wantOrder[i] {
			t.Errorf("listed[%d] = %s, want %s (full: %+v)", i, b.Id, wantOrder[i], listed.Records)
		}
	}
}

// TestServer_Blocks_SSE_BodyBlocks subscribes to dataset=body_blocks
// and observes a create + a patch + a delete event in that order.
func TestServer_Blocks_SSE_BodyBlocks(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	frames := make(chan client.SSEFrame, 32)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- cl.StreamSubscribeObject(streamCtx, spaceId, objectId, editor.Dataset, func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-streamCtx.Done():
			}
			return nil
		})
	}()

	if got := waitFrame(t, frames, 5*time.Second); got.Event != "ready" {
		t.Fatalf("first frame = %q, want ready", got.Event)
	}

	created := blocksCreate(t, e, base, `{"type":"paragraph","text":"hello"}`)

	// Expect a `changes` frame with a $set op on the new block.
	createEvt := awaitChangesEvent(t, frames, editor.Dataset, created.Id)
	if createEvt.VersionId == "" {
		t.Errorf("create event missing versionId")
	}
	if len(createEvt.Records) == 0 {
		t.Fatalf("create event has no records")
	}
	if createEvt.Records[0].Deleted {
		t.Errorf("expected create event, got deleted=true")
	}

	// Patch and observe.
	rec := doJSON(t, e, http.MethodPatch, base+"/editor/blocks/"+created.Id, `{"set":{"text":"hello edited"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	patchEvt := awaitChangesEvent(t, frames, editor.Dataset, created.Id)
	if patchEvt.VersionId == createEvt.VersionId {
		t.Errorf("patch event reused versionId %q", patchEvt.VersionId)
	}
	if patchEvt.Records[0].Deleted {
		t.Errorf("patch should not be deleted")
	}

	// Delete and observe.
	rec = doJSON(t, e, http.MethodDelete, base+"/editor/blocks/"+created.Id, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	deleteEvt := awaitChangesEvent(t, frames, editor.Dataset, created.Id)
	if !deleteEvt.Records[0].Deleted {
		t.Errorf("expected deleted=true on delete event, got %+v", deleteEvt.Records[0])
	}

	streamCancel()
	select {
	case <-streamErr:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit after cancel")
	}
}

// TestServer_Markdown_ConvergesWithBlocks confirms the markdown PUT
// path produces equivalent state to a sequence of block CRUD calls.
// Two objects, same target document, different write strategies — at
// the end the rendered markdown matches and the block trees agree.
func TestServer_Markdown_ConvergesWithBlocks(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, viaMarkdownId := setupBlocksFixture(t, e)
	_, viaBlocksId := setupBlocksFixture(t, e)
	// setupBlocksFixture re-creates the space each call; we only need
	// one space. Re-use the first space id for both objects.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second object: %d %s", rec.Code, rec.Body.String())
	}
	var second api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	viaBlocksId = second.ObjectId

	// Path A: markdown PUT.
	doc := "# Heading 1\n\nparagraph body\n\n## Heading 2\n\nmore body"
	body, _ := json.Marshal(map[string]string{"content": doc})
	rec = doJSON(t, e, http.MethodPut,
		"/v1/spaces/"+spaceId+"/objects/"+viaMarkdownId+"/editor/markdown", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT markdown: %d %s", rec.Code, rec.Body.String())
	}

	// Path B: equivalent series of POST /blocks calls.
	mdBase := "/v1/spaces/" + spaceId + "/objects/" + viaBlocksId
	blocksCreate(t, e, mdBase, `{"type":"heading","style":{"level":1},"text":"Heading 1"}`)
	blocksCreate(t, e, mdBase, `{"type":"paragraph","text":"paragraph body"}`)
	blocksCreate(t, e, mdBase, `{"type":"heading","style":{"level":2},"text":"Heading 2"}`)
	blocksCreate(t, e, mdBase, `{"type":"paragraph","text":"more body"}`)

	// Compare via GET markdown — both should render to the same bytes.
	mdViaPut := getMarkdown(t, e, "/v1/spaces/"+spaceId+"/objects/"+viaMarkdownId+"/editor/markdown")
	mdViaBlocks := getMarkdown(t, e, "/v1/spaces/"+spaceId+"/objects/"+viaBlocksId+"/editor/markdown")
	if mdViaPut != mdViaBlocks {
		t.Errorf("markdown content diverged\nvia PUT:\n%s\nvia blocks:\n%s", mdViaPut, mdViaBlocks)
	}
	if mdViaPut != doc {
		t.Errorf("markdown PUT round-trip lost bytes\nwant: %q\ngot:  %q", doc, mdViaPut)
	}
}

// TestServer_Blocks_MultiRecordBatchId exercises the PATCH/DELETE
// routes against a block whose SDK-derived id carries the multi-
// record-batch suffix (`<base58>:<N>`). The markdown PUT path
// batches inserts into one ModifyBatch, so any 2+-block PUT produces
// at least one suffixed id. The `:` separator is path-segment-safe,
// so the standard `:blockId` route param matches without any
// special handling — this test guards against a regression where the
// separator changes back to `/` (which the echo router would split).
func TestServer_Blocks_MultiRecordBatchId(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// Markdown PUT with three top-level blocks → one ModifyBatch
	// containing three inserts → the 2nd and 3rd records get `:1`,
	// `:2` suffixes on the same ChangeId-derived base.
	doc := "one\n\ntwo\n\nthree"
	body, _ := json.Marshal(map[string]string{"content": doc})
	rec := doJSON(t, e, http.MethodPut, base+"/editor/markdown", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT markdown: %d %s", rec.Code, rec.Body.String())
	}

	listed := blocksList(t, e, base)
	if len(listed.Records) != 3 {
		t.Fatalf("list: %d records, want 3", len(listed.Records))
	}
	var suffixedId string
	for _, b := range listed.Records {
		if strings.Contains(b.Id, ":") {
			suffixedId = b.Id
			break
		}
		if strings.ContainsAny(b.Id, "/") {
			t.Fatalf("block id %q contains `/` — separator regressed; routes will 404", b.Id)
		}
	}
	if suffixedId == "" {
		ids := make([]string, 0, len(listed.Records))
		for _, b := range listed.Records {
			ids = append(ids, b.Id)
		}
		t.Fatalf("no `:`-suffixed block id from batched markdown PUT; ids=%v", ids)
	}

	rec = doJSON(t, e, http.MethodPatch,
		base+"/editor/blocks/"+suffixedId, `{"set":{"text":"patched"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH suffixed id: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodDelete, base+"/editor/blocks/"+suffixedId, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE suffixed id: %d %s", rec.Code, rec.Body.String())
	}

	listed = blocksList(t, e, base)
	for _, b := range listed.Records {
		if b.Id == suffixedId {
			t.Fatalf("block %s not tombstoned, still present", suffixedId)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func setupBlocksFixture(t *testing.T, e http.Handler) (spaceId, objectId string) {
	t.Helper()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"BlockTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	spaceId = sp.Id

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	objectId = obj.ObjectId
	return spaceId, objectId
}

func blocksCreate(t *testing.T, e http.Handler, base, body string) api.Block {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, base+"/editor/blocks", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create block %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var b api.Block
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode block: %v", err)
	}
	return b
}

func blocksList(t *testing.T, e http.Handler, base string) api.BlockListResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, base+"/editor/blocks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list blocks: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.BlockListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return resp
}

func getMarkdown(t *testing.T, e http.Handler, path string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET markdown: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode markdown: %v", err)
	}
	return resp.Content
}

// awaitChangesEvent pulls SSE frames until it finds a `changes` frame
// whose batch contains an event for (dataset, recordId). Other
// `changes` frames are consumed and discarded; non-changes frames
// (ready, lagged, keepalive) are also consumed. Times out after 5s.
func awaitChangesEvent(t *testing.T, frames <-chan client.SSEFrame, dataset, recordId string) api.SubscribeEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
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
				if ev.Dataset != dataset {
					continue
				}
				for _, rec := range ev.Records {
					if rec.Id == recordId {
						return ev
					}
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for changes event on %s/%s", dataset, recordId)
		}
	}
}
