package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/chat"
	"github.com/anyproto/any/internal/editor"
	"github.com/anyproto/any/internal/index"
)

// collectChunks drains a chunker over objectId past `since`, returning
// the entries in yield order and the max ApplySeq seen (0 if none).
func collectChunks(t *testing.T, ctx context.Context, ch index.Chunker, sp space.Space, objectId string, since uint64) ([]index.IndexEntry, uint64) {
	t.Helper()
	var out []index.IndexEntry
	var max uint64
	err := ch.ChunksSince(ctx, sp, objectId, since, func(e index.IndexEntry) error {
		out = append(out, e)
		if e.ApplySeq > max {
			max = e.ApplySeq
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChunksSince(%d) on %s: %v", since, objectId, err)
	}
	// Entries must arrive ascending by ApplySeq.
	for i := 1; i < len(out); i++ {
		if out[i-1].ApplySeq > out[i].ApplySeq {
			t.Errorf("entries not ascending by ApplySeq: %d then %d", out[i-1].ApplySeq, out[i].ApplySeq)
		}
	}
	return out, max
}

// mustModify posts a write and returns the decoded ModifyResult.
func mustModify(t *testing.T, e http.Handler, method, path, body string, wantCode int) api.ModifyResult {
	t.Helper()
	rec := doJSON(t, e, method, path, body)
	if rec.Code != wantCode {
		t.Fatalf("%s %s: status %d body=%s", method, path, rec.Code, rec.Body.String())
	}
	var res api.ModifyResult
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode ModifyResult: %v body=%s", err, rec.Body.String())
		}
	}
	return res
}

func mustCreateObject(t *testing.T, e http.Handler, spaceId, body string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	return obj.ObjectId
}

// TestIndexChunkers_FullFlow drives the three chunkers in-process
// against a live SDK: creation, cursor advance, deletion (tombstones),
// and the non-memory negative case. See PLAN / docs/13-index.md.
func TestIndexChunkers_FullFlow(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	// --- Space ---
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"IndexTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	spaceId := sp.Id

	// --- Chat object + 2 messages ---
	chatObj := mustCreateObject(t, e, spaceId, `{}`)
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj
	msg1 := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", `{"text":"hello one"}`, http.StatusCreated)
	msg2 := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", `{"text":"hello two"}`, http.StatusCreated)
	msg1Id, msg2Id := msg1.RecordIds[0], msg2.RecordIds[0]

	// --- Editor object + 2 blocks ---
	edObj := mustCreateObject(t, e, spaceId, `{}`)
	edBase := "/v1/spaces/" + spaceId + "/objects/" + edObj
	blk1 := mustModify(t, e, http.MethodPost, edBase+"/editor/blocks", `{"type":"paragraph","text":"block one"}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, edBase+"/editor/blocks", `{"type":"paragraph","text":"block two"}`, http.StatusCreated)
	blk1Id := blk1.RecordIds[0]

	// --- Indexed properties: type with meta {"index":"agent"} flags ---
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"Memory","xKey":"memory"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create memory type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	memTypeId := typeResp.TypeId

	propId := map[string]string{}
	addProp := func(body, xkey string) {
		rec = doJSON(t, e, http.MethodPost,
			"/v1/spaces/"+spaceId+"/types/"+memTypeId+"/properties", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create prop %s: %d %s", xkey, rec.Code, rec.Body.String())
		}
		var pr api.AddPropertyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
			t.Fatalf("decode prop %s: %v", xkey, err)
		}
		propId[xkey] = pr.PropId
	}
	for _, xkey := range []string{"context", "keywords", "entities"} {
		addProp(fmt.Sprintf(`{"name":%q,"kind":"string","xKey":%q,"meta":{"index":"agent"}}`, xkey, xkey), xkey)
	}
	// Default-on props: no meta ⇒ scope "props"; "none" opts out.
	addProp(`{"name":"Author","kind":"string","xKey":"author"}`, "author")
	addProp(`{"name":"Score","kind":"number","xKey":"score"}`, "score")
	addProp(`{"name":"Secret","kind":"string","xKey":"secret","meta":{"index":"none"}}`, "secret")

	memObj := mustCreateObject(t, e, spaceId, fmt.Sprintf(`{"types":[%q]}`, memTypeId))
	// Set the property values (keyed by propId, as the wire demands).
	mustModify(t, e, http.MethodPost,
		"/v1/spaces/"+spaceId+"/properties/"+memObj+"/set/"+memTypeId,
		fmt.Sprintf(`{"patch":{%q:"ctx body",%q:"kw1 kw2",%q:"Alice Bob",%q:"Frank Herbert",%q:85600,%q:"hidden"}}`,
			propId["context"], propId["keywords"], propId["entities"],
			propId["author"], propId["score"], propId["secret"]),
		http.StatusOK)
	// Set the object name on the built-in `any` type.
	mustModify(t, e, http.MethodPost,
		"/v1/spaces/"+spaceId+"/properties/"+memObj+"/set/any",
		`{"patch":{"name":"My Memory"}}`, http.StatusOK)

	// --- Resolve SDK space + chunkers (exercise the registry wiring) ---
	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}
	chatCh := mustOneChunker(t, d, chat.Dataset)
	edCh := mustOneChunker(t, d, editor.Dataset)
	propCh := mustOneChunker(t, d, index.DatasetProp)

	// --- ChunksSince(0): full content ---
	chatEntries, chatMax := collectChunks(t, ctx, chatCh, sdkSpace, chatObj, 0)
	assertChat(t, chatEntries, chatObj, map[string]string{msg1Id: "hello one", msg2Id: "hello two"})

	// Editor coalesces its two heading-less blocks into ONE window doc,
	// anchored on the first block, text joined by newline.
	edEntries, _ := collectChunks(t, ctx, edCh, sdkSpace, edObj, 0)
	if len(edEntries) != 1 {
		t.Fatalf("editor entries = %d, want 1 coalesced window: %+v", len(edEntries), edEntries)
	}
	if en := edEntries[0]; en.Scope != index.ScopeBasic || en.Dataset != editor.Dataset || en.ObjectId != edObj ||
		en.RecordId != "win_"+blk1Id || en.Data != "block one\nblock two" {
		t.Errorf("editor window entry wrong: %+v", edEntries[0])
	}

	// Prop chunker: name + description built-ins (scope basic, raw) plus
	// one self-describing "<name>: <value>" entry per indexed property —
	// scope-agent overrides and the default-on scope-props pair; the
	// meta.index="none" prop never enters the catalog.
	memEntries, memMax := collectChunks(t, ctx, propCh, sdkSpace, memObj, 0)
	if len(memEntries) != 7 {
		t.Fatalf("prop entries = %d, want 7 (name+description+3 flagged+2 default): %+v", len(memEntries), memEntries)
	}
	byRecord := map[string]index.IndexEntry{}
	for _, en := range memEntries {
		if en.Dataset != index.DatasetProp || en.ObjectId != memObj {
			t.Errorf("prop entry tags wrong: %+v", en)
		}
		byRecord[en.RecordId] = en
	}
	if _, ok := byRecord[propId["secret"]]; ok {
		t.Errorf("meta.index=none prop leaked into the catalog: %+v", byRecord[propId["secret"]])
	}
	if en := byRecord[index.NamePropRecordId]; en.Scope != index.ScopeBasic || en.Data != "My Memory" {
		t.Errorf("name entry wrong: %+v", en)
	}
	if en := byRecord[index.DescriptionPropRecordId]; en.Scope != index.ScopeBasic || en.Data != "" {
		t.Errorf("description entry wrong (unset ⇒ removal): %+v", en)
	}
	wantProps := map[string]struct{ scope, data string }{
		propId["context"]:  {index.ScopeAgent, "context: ctx body"},
		propId["keywords"]: {index.ScopeAgent, "keywords: kw1 kw2"},
		propId["entities"]: {index.ScopeAgent, "entities: Alice Bob"},
		propId["author"]:   {index.ScopeProps, "Author: Frank Herbert"},
		propId["score"]:    {index.ScopeProps, "Score: 85600"},
	}
	for pid, want := range wantProps {
		en, ok := byRecord[pid]
		if !ok || en.Scope != want.scope || en.Data != want.data {
			t.Errorf("prop %s entry = %+v, want Data %q scope %q", pid, en, want.data, want.scope)
		}
	}

	// --- Cursor: ChunksSince(max) empty, then one more message ---
	if empty, _ := collectChunks(t, ctx, chatCh, sdkSpace, chatObj, chatMax); len(empty) != 0 {
		t.Errorf("ChunksSince(chatMax) not empty: %+v", empty)
	}
	msg3 := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", `{"text":"hello three"}`, http.StatusCreated)
	newEntries, chatMax2 := collectChunks(t, ctx, chatCh, sdkSpace, chatObj, chatMax)
	if len(newEntries) != 1 || newEntries[0].RecordId != msg3.RecordIds[0] || newEntries[0].Data != "hello three" {
		t.Fatalf("incremental chat entries wrong: %+v", newEntries)
	}

	// --- Deletion: chat message → tombstone (Data "") ---
	doJSONExpect(t, e, http.MethodDelete, chatBase+"/chat/messages/"+msg1Id, http.StatusOK)
	delEntries, _ := collectChunks(t, ctx, chatCh, sdkSpace, chatObj, chatMax2)
	assertTombstone(t, delEntries, msg1Id, chatMax2)

	// --- Deletion: editor block → window reconcile ---
	// Editor is a Reconciler: deleting a block doesn't stream a per-record
	// tombstone; the whole object's windows are rebuilt (PrefixDelete +
	// the surviving block's window). The anchor moves to the survivor.
	doJSONExpect(t, e, http.MethodDelete, edBase+"/editor/blocks/"+blk1Id, http.StatusOK)
	rc, ok := edCh.(index.Reconciler)
	if !ok {
		t.Fatal("editor chunker should implement index.Reconciler")
	}
	recon, err := rc.Reconcile(ctx, sdkSpace, edObj, 0)
	if err != nil {
		t.Fatalf("editor reconcile after delete: %v", err)
	}
	if len(recon) != 1 || recon[0].Data != "block two" {
		t.Fatalf("editor reconcile = %+v, want one window 'block two'", recon)
	}

	// --- Deletion: memory object → no entries from the prop chunker ---
	// (structural eviction is the indexer's job — the advance loop
	// prefix-deletes objectId: for tombstoned rows and never calls
	// chunkers; called directly, the chunker skips the tombstone.)
	doJSONExpect(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+memObj, http.StatusNoContent)
	memDel, _ := collectChunks(t, ctx, propCh, sdkSpace, memObj, memMax)
	if len(memDel) != 0 {
		t.Fatalf("prop chunker on tombstoned object = %+v, want nothing", memDel)
	}

	// --- Cursor sanity via Changes() ---
	maxSeq, err := sdkSpace.Changes().MaxApplySeq(ctx)
	if err != nil {
		t.Fatalf("MaxApplySeq: %v", err)
	}
	if maxSeq == 0 {
		t.Errorf("MaxApplySeq = 0")
	}
	changed, err := sdkSpace.Changes().ChangedSince(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ChangedSince: %v", err)
	}
	if !sort.SliceIsSorted(changed, func(i, j int) bool { return changed[i].ApplySeq < changed[j].ApplySeq }) {
		t.Errorf("ChangedSince not ascending")
	}
	ids := map[string]bool{}
	for _, ch := range changed {
		ids[ch.ObjectId] = true
	}
	for _, want := range []string{chatObj, edObj} {
		if !ids[want] {
			t.Errorf("ChangedSince missing object %s", want)
		}
	}

	// --- Non-memory object: every prop entry is a removal (Data "") ---
	// chatObj has no name/description and doesn't carry the flagged
	// type, so the prop chunker emits idempotent removals for all seven
	// records — cleared values and detached types evict record-level.
	liveNonMem, _ := collectChunks(t, ctx, propCh, sdkSpace, chatObj, 0)
	if len(liveNonMem) != 7 {
		t.Fatalf("non-memory prop entries = %d, want 7 removals: %+v", len(liveNonMem), liveNonMem)
	}
	for _, en := range liveNonMem {
		if en.Data != "" {
			t.Errorf("non-memory entry should be a removal: %+v", en)
		}
	}
}

func mustOneChunker(t *testing.T, d *deps, dataset string) index.Chunker {
	t.Helper()
	chs := d.chunkers.ForDataset(dataset)
	if len(chs) != 1 {
		t.Fatalf("registry ForDataset(%q) = %d chunkers, want 1", dataset, len(chs))
	}
	return chs[0]
}

func doJSONExpect(t *testing.T, e http.Handler, method, path string, wantCode int) {
	t.Helper()
	rec := doJSON(t, e, method, path, "")
	if rec.Code != wantCode {
		t.Fatalf("%s %s: status %d body=%s", method, path, rec.Code, rec.Body.String())
	}
}

func assertChat(t *testing.T, entries []index.IndexEntry, objectId string, want map[string]string) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("chat entries = %d, want %d: %+v", len(entries), len(want), entries)
	}
	for _, en := range entries {
		if en.Scope != index.ScopeChat || en.Dataset != chat.Dataset || en.ObjectId != objectId {
			t.Errorf("chat entry tags wrong: %+v", en)
		}
		w, ok := want[en.RecordId]
		if !ok {
			t.Errorf("unexpected chat record %s", en.RecordId)
			continue
		}
		if en.Data != w {
			t.Errorf("chat %s Data = %q, want %q", en.RecordId, en.Data, w)
		}
	}
}

func assertTombstone(t *testing.T, entries []index.IndexEntry, recordId string, sinceSeq uint64) {
	t.Helper()
	if len(entries) != 1 {
		t.Fatalf("tombstone stream = %d entries, want 1: %+v", len(entries), entries)
	}
	en := entries[0]
	if en.RecordId != recordId {
		t.Errorf("tombstone recordId = %s, want %s", en.RecordId, recordId)
	}
	if en.Data != "" {
		t.Errorf("tombstone Data = %q, want empty", en.Data)
	}
	if en.ApplySeq <= sinceSeq {
		t.Errorf("tombstone ApplySeq %d not past cursor %d", en.ApplySeq, sinceSeq)
	}
}
