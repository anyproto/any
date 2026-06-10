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
// the entries in yield order and the max AddSeq seen (0 if none).
func collectChunks(t *testing.T, ctx context.Context, ch index.Chunker, sp space.Space, objectId string, since uint64) ([]index.IndexEntry, uint64) {
	t.Helper()
	var out []index.IndexEntry
	var max uint64
	err := ch.ChunksSince(ctx, sp, objectId, since, func(e index.IndexEntry) error {
		out = append(out, e)
		if e.AddSeq > max {
			max = e.AddSeq
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChunksSince(%d) on %s: %v", since, objectId, err)
	}
	// Entries must arrive ascending by AddSeq.
	for i := 1; i < len(out); i++ {
		if out[i-1].AddSeq > out[i].AddSeq {
			t.Errorf("entries not ascending by AddSeq: %d then %d", out[i-1].AddSeq, out[i].AddSeq)
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

	// --- Agent memory: type + props + object ---
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"Memory","xKey":"agent_memory"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create memory type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	memTypeId := typeResp.TypeId

	propId := map[string]string{}
	for _, xkey := range []string{"context", "keywords", "entities"} {
		rec = doJSON(t, e, http.MethodPost,
			"/v1/spaces/"+spaceId+"/types/"+memTypeId+"/properties",
			fmt.Sprintf(`{"name":%q,"kind":"string","xKey":%q}`, xkey, xkey))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create prop %s: %d %s", xkey, rec.Code, rec.Body.String())
		}
		var pr api.AddPropertyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
			t.Fatalf("decode prop %s: %v", xkey, err)
		}
		propId[xkey] = pr.PropId
	}

	memObj := mustCreateObject(t, e, spaceId, fmt.Sprintf(`{"types":[%q]}`, memTypeId))
	// Set the three property values (keyed by propId, as the wire demands).
	mustModify(t, e, http.MethodPost,
		"/v1/spaces/"+spaceId+"/properties/"+memObj+"/base/"+memTypeId,
		fmt.Sprintf(`{"patch":{%q:"ctx body",%q:"kw1 kw2",%q:"Alice Bob"}}`,
			propId["context"], propId["keywords"], propId["entities"]),
		http.StatusOK)
	// Set the object name on the built-in `any` type.
	mustModify(t, e, http.MethodPost,
		"/v1/spaces/"+spaceId+"/properties/"+memObj+"/base/any",
		`{"patch":{"name":"My Memory"}}`, http.StatusOK)

	// --- Resolve SDK space + chunkers (exercise the registry wiring) ---
	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}
	chatCh := mustOneChunker(t, d, chat.Dataset)
	edCh := mustOneChunker(t, d, editor.Dataset)
	memCh := mustOneChunker(t, d, index.DatasetObjects)

	// --- ChunksSince(0): full content ---
	chatEntries, chatMax := collectChunks(t, ctx, chatCh, sdkSpace, chatObj, 0)
	assertChat(t, chatEntries, chatObj, map[string]string{msg1Id: "hello one", msg2Id: "hello two"})

	edEntries, edMax := collectChunks(t, ctx, edCh, sdkSpace, edObj, 0)
	if len(edEntries) != 2 {
		t.Fatalf("editor entries = %d, want 2: %+v", len(edEntries), edEntries)
	}
	for _, en := range edEntries {
		if en.Scope != index.ScopeBasic || en.Dataset != editor.Dataset || en.ObjectId != edObj {
			t.Errorf("editor entry tags wrong: %+v", en)
		}
		if en.Data != "block one" && en.Data != "block two" {
			t.Errorf("editor entry data unexpected: %q", en.Data)
		}
	}

	memEntries, memMax := collectChunks(t, ctx, memCh, sdkSpace, memObj, 0)
	if len(memEntries) != 1 {
		t.Fatalf("memory entries = %d, want 1: %+v", len(memEntries), memEntries)
	}
	me := memEntries[0]
	if me.Scope != index.ScopeAgent || me.Dataset != index.DatasetObjects || me.RecordId != memObj {
		t.Errorf("memory entry tags wrong: %+v", me)
	}
	if me.Data != "My Memory\nctx body\nkw1 kw2\nAlice Bob" {
		t.Errorf("memory Data = %q", me.Data)
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

	// --- Deletion: editor block → tombstone ---
	doJSONExpect(t, e, http.MethodDelete, edBase+"/editor/blocks/"+blk1Id, http.StatusOK)
	edDel, _ := collectChunks(t, ctx, edCh, sdkSpace, edObj, edMax)
	assertTombstone(t, edDel, blk1Id, edMax)

	// --- Deletion: memory object → agent tombstone ---
	doJSONExpect(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+memObj, http.StatusNoContent)
	memDel, _ := collectChunks(t, ctx, memCh, sdkSpace, memObj, memMax)
	assertTombstone(t, memDel, memObj, memMax)

	// --- Cursor sanity via Changes() ---
	maxSeq, err := sdkSpace.Changes().MaxAddSeq(ctx)
	if err != nil {
		t.Fatalf("MaxAddSeq: %v", err)
	}
	if maxSeq == 0 {
		t.Errorf("MaxAddSeq = 0")
	}
	changed, err := sdkSpace.Changes().ChangedSince(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ChangedSince: %v", err)
	}
	if !sort.SliceIsSorted(changed, func(i, j int) bool { return changed[i].AddSeq < changed[j].AddSeq }) {
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

	// --- Non-memory object yields a tombstone for its live row ---
	// chatObj is a regular (chat) object, not agent_memory-typed. The agent
	// chunker still emits an idempotent removal entry (Data "") for it: the
	// indexer can't distinguish "row didn't stream" from "streamed but not
	// a memory", so removal is signalled explicitly per streamed row.
	liveNonMem, _ := collectChunks(t, ctx, memCh, sdkSpace, chatObj, 0)
	assertTombstone(t, liveNonMem, chatObj, 0)
	// ...but its delete still produces a tombstone (idempotent removal —
	// the indexer no-ops on a never-indexed id).
	beforeDel, err := sdkSpace.Changes().MaxAddSeq(ctx)
	if err != nil {
		t.Fatalf("MaxAddSeq before del: %v", err)
	}
	doJSONExpect(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+chatObj, http.StatusNoContent)
	nonMemDel, _ := collectChunks(t, ctx, memCh, sdkSpace, chatObj, beforeDel)
	assertTombstone(t, nonMemDel, chatObj, beforeDel)
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
	if en.AddSeq <= sinceSeq {
		t.Errorf("tombstone AddSeq %d not past cursor %d", en.AddSeq, sinceSeq)
	}
}
