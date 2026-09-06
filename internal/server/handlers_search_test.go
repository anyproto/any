//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/indexer"
)

// fakeEmbedder is a deterministic bag-of-words embedder: each word
// hashes to a dimension, vectors are L2-normalized. Shared words ⇒
// cosine similarity, no external service.
type fakeEmbedder struct{ dim int }

func (f fakeEmbedder) vec(text string) []float32 {
	v := make([]float32, f.dim)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%uint32(f.dim)]++
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm > 0 {
		n := float32(math.Sqrt(norm))
		for i := range v {
			v[i] /= n
		}
	}
	return v
}

func (f fakeEmbedder) EmbedDocs(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.vec(t)
	}
	return out, nil
}

func (f fakeEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return f.vec(text), nil
}

func (f fakeEmbedder) Dim(context.Context) (int, error) { return f.dim, nil }

// newTestIndexer wires an in-memory indexer into d. Returns it for
// direct Sync calls.
func newTestIndexer(t *testing.T, d *deps, emb indexer.Embedder) *indexer.Indexer {
	t.Helper()
	dim := 0
	if emb != nil {
		var err error
		dim, err = emb.Dim(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	st, err := indexer.OpenStoreInMemory(context.Background(), dim, emb != nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(d.sdk, d.eng.chunkers, st, indexer.Options{
		Embedder: emb,
		Debounce: 10 * time.Millisecond,
	})
	d.indexer = ix
	return ix
}

func doSearch(t *testing.T, e http.Handler, spaceId string, req api.SearchRequest, wantCode int) api.SearchResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/search", string(body))
	if rec.Code != wantCode {
		t.Fatalf("search: status %d body=%s", rec.Code, rec.Body.String())
	}
	var res api.SearchResponse
	if wantCode == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode search response: %v", err)
		}
	}
	return res
}

func hitRecordIds(res api.SearchResponse) []string {
	out := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, h.RecordId)
	}
	return out
}

// TestSearch_FullFlow drives the indexer + search endpoint in-process:
// content lands via the real handlers, the indexer Syncs synchronously,
// and the endpoint answers in every mode (with the fake embedder).
func TestSearch_FullFlow(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, fakeEmbedder{dim: 16})
	defer func() { _ = ix.Close() }()

	// --- Space + content ---
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SearchTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	spaceId := sp.Id

	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj
	msg := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"the zeppelin disaster of 1937"}`, http.StatusCreated)
	msgId := msg.RecordIds[0]
	mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"lunch plans for tomorrow"}`, http.StatusCreated)

	edObj := mustCreateModuleObject(t, e, spaceId, "editor")
	edBase := "/v1/spaces/" + spaceId + "/objects/" + edObj
	blk := mustModify(t, e, http.MethodPost, edBase+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"quarterly budget review notes"}`, http.StatusCreated)
	blkId := blk.RecordIds[0]

	// --- Before sync: valid request, empty index ---
	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin"}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Fatalf("pre-sync hits = %+v, want none", res.Hits)
	}

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	// --- FTS mode ---
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin", Mode: api.SearchModeFTS}, http.StatusOK)
	if res.Mode != api.SearchModeFTS || len(res.Hits) != 1 || res.Hits[0].RecordId != msgId {
		t.Fatalf("fts: mode=%s hits=%v", res.Mode, hitRecordIds(res))
	}
	if res.VectorStatus != api.VectorStatusSkipped {
		t.Errorf("fts request with embedder: vectorStatus = %s, want skipped", res.VectorStatus)
	}
	if res.Hits[0].Scope != "chat" || res.Hits[0].ObjectId != chatObj || res.Hits[0].Data == "" {
		t.Errorf("fts hit shape wrong: %+v", res.Hits[0])
	}

	// --- Vector mode (fake embedder; bag-of-words overlap) ---
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin disaster", Mode: api.SearchModeVector}, http.StatusOK)
	if res.Mode != api.SearchModeVector || len(res.Hits) == 0 || res.Hits[0].RecordId != msgId {
		t.Fatalf("vector: mode=%s hits=%v", res.Mode, hitRecordIds(res))
	}

	// --- Hybrid (default) ---
	// Editor blocks index as coalesced windows: the hit's recordId is the
	// window anchor (win_<firstBlockId>), and it resolves to the editor
	// object under scope "basic".
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "quarterly budget"}, http.StatusOK)
	if res.Mode != api.SearchModeHybrid || len(res.Hits) == 0 || res.Hits[0].RecordId != "win_"+blkId {
		t.Fatalf("hybrid: mode=%s hits=%v", res.Mode, hitRecordIds(res))
	}
	if res.Hits[0].ObjectId != edObj || res.Hits[0].Scope != "basic" {
		t.Errorf("hybrid editor hit shape wrong: %+v", res.Hits[0])
	}
	if res.VectorStatus != api.VectorStatusUsed {
		t.Errorf("hybrid with working embedder: vectorStatus = %s, want used", res.VectorStatus)
	}

	// --- Scope filter excludes the chat hit ---
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin", Scopes: []string{"basic"}}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Fatalf("scoped search leaked: %+v", res.Hits)
	}

	// --- limit counts records; passages on request ---
	// A chunked message: several index docs, one hit.
	long := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"`+strings.Repeat("zeppelin lore chapter. ", 260)+`"}`, http.StatusCreated)
	longId := long.RecordIds[0]
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin", Mode: api.SearchModeFTS, Limit: 1}, http.StatusOK)
	if len(res.Hits) != 1 {
		t.Fatalf("limit 1: hits = %v, want one record", hitRecordIds(res))
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if ids := hitRecordIds(res); len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("limit 10: hits = %v, want the two records once each", ids)
	}
	rawRec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/search", `{"query":"zeppelin","mode":"fts","limit":10}`)
	if strings.Contains(rawRec.Body.String(), `"passages"`) {
		t.Fatalf("passages must be absent unless asked: %s", rawRec.Body.String())
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin", Mode: api.SearchModeFTS, Limit: 10, Passages: 3, MaxData: 30}, http.StatusOK)
	var longHit *api.SearchHit
	for i := range res.Hits {
		if res.Hits[i].RecordId == longId {
			longHit = &res.Hits[i]
		}
	}
	if longHit == nil || len(longHit.Passages) < 1 || len(longHit.Passages) > 3 {
		t.Fatalf("passages 3: %+v", res.Hits)
	}
	chunks := map[int]bool{longHit.Chunk: true}
	for _, p := range longHit.Passages {
		if chunks[p.Chunk] || p.Data == "" || p.DataTotal == 0 {
			t.Fatalf("bad passage %+v (hit chunk %d)", p, longHit.Chunk)
		}
		chunks[p.Chunk] = true
	}
	rawRec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/search", `{"query":"zeppelin","passages":11}`)
	if rawRec.Code != http.StatusBadRequest || !strings.Contains(rawRec.Body.String(), `"request.invalid_field"`) || !strings.Contains(rawRec.Body.String(), `"passages"`) {
		t.Fatalf("passages 11: %d %s", rawRec.Code, rawRec.Body.String())
	}

	// --- Object deletion purges its docs ---
	doJSONExpect(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+chatObj, http.StatusNoContent)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin"}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Fatalf("hits after object delete = %v, want none", hitRecordIds(res))
	}

	// --- Validation ---
	doSearch(t, e, spaceId, api.SearchRequest{}, http.StatusBadRequest)
	doSearch(t, e, spaceId, api.SearchRequest{Query: "x", Mode: "fuzzy"}, http.StatusBadRequest)
	// Scopes are an open set: an unknown-but-valid slug is fine (no
	// hits), only a malformed slug is a 400.
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "zeppelin", Scopes: []string{"everything"}}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Errorf("unknown scope should return no hits: %v", hitRecordIds(res))
	}
	doSearch(t, e, spaceId, api.SearchRequest{Query: "x", Scopes: []string{"bad scope!"}}, http.StatusBadRequest)
}

func TestSearch_NoEmbedderAndDisabled(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SearchDegraded"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	spaceId := sp.Id

	// indexer disabled (nil) → 409
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/search", `{"query":"x"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("disabled: status %d body=%s", rec.Code, rec.Body.String())
	}

	// FTS-only indexer: vector → 400, hybrid degrades to fts.
	ix := newTestIndexer(t, d, nil)
	defer func() { _ = ix.Close() }()

	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+chatObj+"/chat/messages",
		`{"text":"orbital mechanics primer"}`, http.StatusCreated)
	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	doSearch(t, e, spaceId, api.SearchRequest{Query: "x", Mode: api.SearchModeVector}, http.StatusBadRequest)

	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "orbital"}, http.StatusOK)
	if res.Mode != api.SearchModeFTS {
		t.Errorf("hybrid should degrade to fts without embedder, got %s", res.Mode)
	}
	if len(res.Hits) != 1 {
		t.Errorf("degraded hybrid hits = %v", hitRecordIds(res))
	}
	if res.VectorStatus != api.VectorStatusDisabled {
		t.Errorf("no embedder configured: vectorStatus = %s, want disabled", res.VectorStatus)
	}
}

// TestSearch_WorkerPath exercises the asynchronous pipeline: Start()
// discovers the space (space-list subscribe), the change-feed dirty
// signal triggers a debounced advance, and the embed loop lands
// vectors — all observed by polling the endpoint.
func TestSearch_WorkerPath(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, fakeEmbedder{dim: 16})
	defer func() { _ = ix.Close() }()
	ix.Start(ctx)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SearchWorker"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	spaceId := sp.Id

	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+chatObj+"/chat/messages",
		`{"text":"asynchronous worker pipeline check"}`, http.StatusCreated)

	deadline := time.Now().Add(15 * time.Second)
	for {
		res := doSearch(t, e, spaceId, api.SearchRequest{Query: "asynchronous pipeline", Mode: api.SearchModeFTS}, http.StatusOK)
		if len(res.Hits) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker never indexed the message; last hits = %v", hitRecordIds(res))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
