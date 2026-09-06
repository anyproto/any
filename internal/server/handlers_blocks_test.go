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
	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/editor"
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
	initial := blocksList(t, e, base)
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
	rec := doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+first.Id, patchBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	patchResp := decodeModifyResult(t, rec.Body.Bytes())
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
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+first.Id,
		`{"unset":["style"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH unset: %d %s", rec.Code, rec.Body.String())
	}
	listed = blocksList(t, e, base)
	if listed.Records[0].Style != nil {
		t.Errorf("post-unset style should be nil, got %+v", listed.Records[0].Style)
	}

	// 6. Delete the second block — now returns 200 with a ModifyResult.
	rec = doJSON(t, e, http.MethodDelete, base+"/editor/editor_blocks/blocks/"+second.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	if res := decodeModifyResult(t, rec.Body.Bytes()); res.VersionId == "" {
		t.Errorf("delete: empty versionId in %+v", res)
	}

	listed = blocksList(t, e, base)
	if len(listed.Records) != 1 {
		t.Fatalf("post-delete list: %d records, want 1", len(listed.Records))
	}
	if listed.Records[0].Id != first.Id {
		t.Errorf("post-delete remaining id = %s, want %s", listed.Records[0].Id, first.Id)
	}

	// 7. Patching a deleted block returns 404.
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+second.Id, `{"set":{"text":"resurrect"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH deleted: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// TestServer_Blocks_NestedTree confirms parentId/pos navigation: a
// block with parentId=<other.Id> reports the right parent and
// children sort by nav.pos. With the GET /blocks endpoint gone the
// test now goes through POST /query — the same path real clients
// take to reconstruct the tree (one query per parent for children).
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

	byId := map[string]block{}
	for _, b := range listed.Records {
		byId[b.Id] = b
	}
	if byId[parent.Id].Nav.ParentId != "" {
		t.Errorf("parent.ParentId = %q, want empty", byId[parent.Id].Nav.ParentId)
	}
	if byId[childA.Id].Nav.ParentId != parent.Id || byId[childB.Id].Nav.ParentId != parent.Id {
		t.Errorf("children parentId mismatch: a=%q b=%q want %q",
			byId[childA.Id].Nav.ParentId, byId[childB.Id].Nav.ParentId, parent.Id)
	}
	if !(byId[childA.Id].Nav.Pos < byId[childB.Id].Nav.Pos) {
		t.Errorf("children pos not ascending: a=%q b=%q",
			byId[childA.Id].Nav.Pos, byId[childB.Id].Nav.Pos)
	}
}

// TestServer_Blocks_QuerySubscribe subscribes to the per-object
// editor_blocks dataset via POST /v1/spaces/:id/query/subscribe and
// observes a create as Added, a patch as Updated, and a delete as
// Removed. Same windowed shape every consumer sees.
func TestServer_Blocks_QuerySubscribe(t *testing.T) {
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
	body, _ := json.Marshal(map[string]any{
		"objectId":     objectId,
		"dataset":      editor.Dataset,
		"includeTotal": true,
	})
	go func() {
		streamErr <- cl.StreamQuerySubscribe(streamCtx, spaceId, body, func(f client.SSEFrame) error {
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
	snap := waitFrame(t, frames, 5*time.Second)
	if snap.Event != "snapshot" {
		t.Fatalf("second frame = %q, want snapshot", snap.Event)
	}
	// includeTotal=true → the snapshot frame carries total + hasNext,
	// and hasNext is consistent with the window it returned.
	var snapData api.QuerySubscribeSnapshot
	if err := json.Unmarshal(snap.Data, &snapData); err != nil {
		t.Fatalf("decode snapshot frame: %v", err)
	}
	if snapData.Total == nil || snapData.HasNext == nil {
		t.Fatalf("snapshot frame: total=%v hasNext=%v, want both populated", snapData.Total, snapData.HasNext)
	}
	if want := len(snapData.Records) < *snapData.Total; *snapData.HasNext != want {
		t.Errorf("snapshot hasNext=%v, want %v (records=%d total=%d)",
			*snapData.HasNext, want, len(snapData.Records), *snapData.Total)
	}

	created := blocksCreate(t, e, base, `{"type":"paragraph","text":"hello"}`)

	createEvt := awaitWindowedEvent(t, frames, created.Id, windowedAdded)
	if createEvt.VersionId == "" {
		t.Errorf("create event missing versionId")
	}

	rec := doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+created.Id, `{"set":{"text":"hello edited"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	patchEvt := awaitWindowedEvent(t, frames, created.Id, windowedUpdated)
	if patchEvt.VersionId == createEvt.VersionId {
		t.Errorf("patch event reused versionId %q", patchEvt.VersionId)
	}

	rec = doJSON(t, e, http.MethodDelete, base+"/editor/editor_blocks/blocks/"+created.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	_ = awaitWindowedEvent(t, frames, created.Id, windowedRemoved)

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
	viaBlocksId = mustCreateModuleObject(t, e, spaceId, "editor")

	// Path A: markdown PUT.
	doc := "# Heading 1\n\nparagraph body\n\n## Heading 2\n\nmore body"
	body, _ := json.Marshal(map[string]string{"content": doc})
	rec := doJSON(t, e, http.MethodPut,
		"/v1/spaces/"+spaceId+"/objects/"+viaMarkdownId+"/editor/editor_blocks/markdown", string(body))
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
	mdViaPut := getMarkdown(t, e, "/v1/spaces/"+spaceId+"/objects/"+viaMarkdownId+"/editor/editor_blocks/markdown")
	mdViaBlocks := getMarkdown(t, e, "/v1/spaces/"+spaceId+"/objects/"+viaBlocksId+"/editor/editor_blocks/markdown")
	if mdViaPut != mdViaBlocks {
		t.Errorf("markdown content diverged\nvia PUT:\n%s\nvia blocks:\n%s", mdViaPut, mdViaBlocks)
	}
	if mdViaPut != doc {
		t.Errorf("markdown PUT round-trip lost bytes\nwant: %q\ngot:  %q", doc, mdViaPut)
	}
}

// TestServer_Markdown_Append exercises the append-only fast path:
// POST .../editor/editor_blocks/markdown/append adds blocks at the tail without
// reading or diffing the existing document, and the result renders
// identically to a single PUT of the concatenated document.
func TestServer_Markdown_Append(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// Append onto an empty object — exercises the maxPos=="" seed path.
	appendMarkdown(t, e, base, "# Heading 1\n\nfirst body")
	if got := getMarkdown(t, e, base+"/editor/editor_blocks/markdown"); got != "# Heading 1\n\nfirst body" {
		t.Fatalf("after first append: %q", got)
	}

	// Append again — new blocks land strictly after the existing tail.
	resp := appendMarkdown(t, e, base, "## Heading 2\n\nsecond body")
	if len(resp.Inserted) != 2 {
		t.Errorf("expected 2 inserted ids, got %d (%v)", len(resp.Inserted), resp.Inserted)
	}
	if len(resp.Updated) != 0 || len(resp.Deleted) != 0 {
		t.Errorf("append must not update/delete: updated=%v deleted=%v", resp.Updated, resp.Deleted)
	}

	want := "# Heading 1\n\nfirst body\n\n## Heading 2\n\nsecond body"
	if got := getMarkdown(t, e, base+"/editor/editor_blocks/markdown"); got != want {
		t.Errorf("after second append:\nwant %q\ngot  %q", want, got)
	}

	// Block order is strictly ascending by nav.pos — the appended
	// blocks must sort after the originals.
	listed := blocksList(t, e, base)
	if len(listed.Records) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(listed.Records))
	}
	for i := 1; i < len(listed.Records); i++ {
		if !(listed.Records[i-1].Nav.Pos < listed.Records[i].Nav.Pos) {
			t.Errorf("nav.pos not ascending at %d: %q then %q",
				i, listed.Records[i-1].Nav.Pos, listed.Records[i].Nav.Pos)
		}
	}

	// Empty content is a no-op: 200, nothing inserted, document unchanged.
	resp = appendMarkdown(t, e, base, "")
	if len(resp.Inserted) != 0 {
		t.Errorf("empty append inserted %v", resp.Inserted)
	}
	if got := getMarkdown(t, e, base+"/editor/editor_blocks/markdown"); got != want {
		t.Errorf("empty append changed document: %q", got)
	}
}

func appendMarkdown(t *testing.T, e http.Handler, base, content string) api.MarkdownSetResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	rec := doJSON(t, e, http.MethodPost, base+"/editor/editor_blocks/markdown/append", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST markdown/append: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.MarkdownSetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode append resp: %v", err)
	}
	return resp
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
	rec := doJSON(t, e, http.MethodPut, base+"/editor/editor_blocks/markdown", string(body))
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
		base+"/editor/editor_blocks/blocks/"+suffixedId, `{"set":{"text":"patched"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH suffixed id: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodDelete, base+"/editor/editor_blocks/blocks/"+suffixedId, "")
	if rec.Code != http.StatusOK {
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

	objectId = mustCreateModuleObject(t, e, spaceId, "editor")
	return spaceId, objectId
}

// block is the test-local decode target for an editor_blocks record
// read back via /query. Writes now return api.ModifyResult, so the
// block body is always fetched through the query path.
type block struct {
	Id    string         `json:"id"`
	Ver   map[string]any `json:"_ver"`
	Type  string         `json:"type"`
	Style map[string]any `json:"style"`
	Text  string         `json:"text"`
	Nav   struct {
		ParentId string `json:"parentId"`
		Pos      string `json:"pos"`
	} `json:"nav"`
}

// blocksCreate posts a block and returns the read-back record.
// recordIds[0] from the ModifyResult is the server-derived block id,
// which we re-query to surface nav.pos / _ver / etc.
func blocksCreate(t *testing.T, e http.Handler, base, body string) block {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, base+"/editor/editor_blocks/blocks", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create block %s: %d %s", body, rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if res.VersionId == "" {
		t.Errorf("create block %s: empty versionId in %+v", body, res)
	}
	if len(res.RecordIds) == 0 || res.RecordIds[0] == "" {
		t.Fatalf("create block %s: no recordIds in %+v", body, res)
	}
	id := res.RecordIds[0]
	for _, b := range blocksList(t, e, base).Records {
		if b.Id == id {
			return b
		}
	}
	t.Fatalf("create block %s: %s not found after create", body, id)
	return block{}
}

// blocksListResp is the read-back block list, materialised by POSTing
// /v1/spaces/:id/query with dataset=editor_blocks and sort=nav.pos.
type blocksListResp struct {
	Records []block
}

func blocksList(t *testing.T, e http.Handler, base string) blocksListResp {
	t.Helper()
	parts := strings.SplitN(strings.TrimPrefix(base, "/v1/spaces/"), "/objects/", 2)
	if len(parts) != 2 {
		t.Fatalf("blocksList: cannot parse spaceId/objectId from %q", base)
	}
	spaceId, objectId := parts[0], parts[1]
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId,
		"dataset":  "editor_blocks",
		"sort":     []string{"nav.pos"},
	})
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("query blocks: %d %s", rec.Code, rec.Body.String())
	}
	var qr struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	out := blocksListResp{Records: make([]block, 0, len(qr.Records))}
	for _, raw := range qr.Records {
		var b block
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		out.Records = append(out.Records, b)
	}
	return out
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

type windowedKind int

const (
	windowedAdded windowedKind = iota
	windowedUpdated
	windowedRemoved
)

// awaitWindowedEvent pulls SSE frames until it finds a `changes` frame
// whose batch contains an event for recordId in the requested bucket
// (Added / Updated / Removed). Other `changes` frames are consumed
// and discarded; non-changes frames (ready, snapshot, keepalive) are
// also consumed. Times out after 5s.
func awaitWindowedEvent(t *testing.T, frames <-chan client.SSEFrame, recordId string, kind windowedKind) api.QuerySubscribeEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-frames:
			if f.Event != "changes" {
				continue
			}
			var batch []api.QuerySubscribeEvent
			if err := json.Unmarshal(f.Data, &batch); err != nil {
				t.Fatalf("decode changes batch: %v", err)
			}
			for _, ev := range batch {
				switch kind {
				case windowedAdded:
					for _, r := range ev.Added {
						if r.Id == recordId {
							return ev
						}
					}
				case windowedUpdated:
					for _, r := range ev.Updated {
						if r.Id == recordId {
							return ev
						}
					}
				case windowedRemoved:
					for _, r := range ev.Removed {
						if r.Id == recordId {
							return ev
						}
					}
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for windowed event on %s (kind=%d)", recordId, kind)
		}
	}
}

// waitFrame pulls one frame from ch with a deadline. Tests that
// silently hang on a missed event are painful; a labeled timeout
// turns those into immediate failures.
func waitFrame(t *testing.T, ch <-chan client.SSEFrame, d time.Duration) client.SSEFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(d):
		t.Fatal("timed out waiting for SSE frame")
		return client.SSEFrame{}
	}
}
