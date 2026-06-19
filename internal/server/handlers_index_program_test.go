//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/agentdebug"
	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/program"
)

// mustCreateNamed creates an object with the given types and (optional)
// any.name, returning its id.
func mustCreateNamed(t *testing.T, e http.Handler, spaceId string, types []string, name string) string {
	t.Helper()
	body := map[string]any{}
	if len(types) > 0 {
		body["types"] = types
	}
	if name != "" {
		body["initialProperties"] = map[string]any{"any": map[string]any{"name": name}}
	}
	raw, _ := json.Marshal(body)
	return mustCreateObject(t, e, spaceId, string(raw))
}

// setDatasetRecord writes one record into a dataset via the generic
// /modify endpoint (the path program/debug records take — no bespoke
// handler).
func setDatasetRecord(t *testing.T, e http.Handler, spaceId, objectId, dataset, recordID string, fields map[string]any) {
	t.Helper()
	ops := make([]map[string]any, 0, len(fields))
	for k, v := range fields {
		ops = append(ops, map[string]any{"type": "$set", "path": k, "value": v})
	}
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId,
		"dataset":  dataset,
		"records":  []map[string]any{{"id": recordID, "upsert": true, "ops": ops}},
	})
	r := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", string(body))
	if r.Code != http.StatusOK {
		t.Fatalf("modify %s/%s: %d %s", dataset, recordID, r.Code, r.Body.String())
	}
}

// TestIndexer_ProgramDocsAndDebugExclusion pins the index-scope contract:
//   - program DESCRIPTION + METHODS are searchable (scope "program"), but
//     program SOURCE is never indexed (it is code, not knowledge);
//   - agent_debug_log objects are excluded wholesale — neither their
//     dataset content (no chunker) nor their prompt-derived name (prop
//     chunker exclusion) reaches search;
//   - a normal named object's name IS indexed (the control that proves
//     the exclusion is selective, not a broken harness).
func TestIndexer_ProgramDocsAndDebugExclusion(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough for scope checks
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "ProgDebugScope")

	// Program object: source (excluded) + description + one method.
	progObj := mustCreateNamed(t, e, spaceId, []string{program.TypeId}, "")
	setDatasetRecord(t, e, spaceId, progObj, program.DatasetSource, "main",
		map[string]any{"code": "function uniquesourcetoken(){ return 42 }"})
	setDatasetRecord(t, e, spaceId, progObj, program.DatasetDescription, "main",
		map[string]any{"text": "describes the frobnicator widget tool"})
	setDatasetRecord(t, e, spaceId, progObj, program.DatasetMethods, "doStuff",
		map[string]any{"name": "doStuff(x)", "kind": "getter", "text": "performs the zorble operation", "pos": 0})

	// Debug object WITH a name — the prop chunker must NOT index it.
	dbgObj := mustCreateNamed(t, e, spaceId, []string{agentdebug.TypeId}, "zzdebugname uniquephrase")
	setDatasetRecord(t, e, spaceId, dbgObj, agentdebug.Dataset, "000000_boot",
		map[string]any{"seq": 0, "kind": "boot", "prompt": "debugonly content marker"})

	// Control: a plain named object — its name MUST be indexed (scope basic).
	ctrlObj := mustCreateNamed(t, e, spaceId, nil, "indexablecontrolname")

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	fts := func(q string) api.SearchResponse {
		return doSearch(t, e, spaceId, api.SearchRequest{Query: q, Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	}
	onlyHit := func(what, q, wantScope, wantDataset, wantObj string) {
		t.Helper()
		res := fts(q)
		if len(res.Hits) != 1 {
			t.Fatalf("%s: hits = %v, want exactly 1", what, hitRecordIds(res))
		}
		h := res.Hits[0]
		if h.Scope != wantScope || h.Dataset != wantDataset || h.ObjectId != wantObj {
			t.Fatalf("%s: hit = {scope:%s dataset:%s obj:%s}, want {scope:%s dataset:%s obj:%s}",
				what, h.Scope, h.Dataset, h.ObjectId, wantScope, wantDataset, wantObj)
		}
	}
	noHit := func(what, q string) {
		t.Helper()
		if res := fts(q); len(res.Hits) != 0 {
			t.Fatalf("%s: hits = %v, want none", what, hitRecordIds(res))
		}
	}

	// Program description + methods are searchable under scope "program".
	onlyHit("program description", "frobnicator", program.Scope, program.DatasetDescription, progObj)
	onlyHit("program method body", "zorble", program.Scope, program.DatasetMethods, progObj)
	onlyHit("program method name", "dostuff", program.Scope, program.DatasetMethods, progObj)

	// Program SOURCE is never indexed.
	noHit("program source", "uniquesourcetoken")

	// Control object's name IS indexed (proves names reach the index).
	onlyHit("control name", "indexablecontrolname", "basic", "prop", ctrlObj)

	// Debug object: neither its name nor its dataset content is searchable.
	noHit("debug object name", "zzdebugname")
	noHit("debug dataset content", "debugonly")
}
