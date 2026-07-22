// Agent data-layer coverage through the real binary: turn append /
// chunk create on a chat object (agent_log datasets), the chunk →
// turns drill-down range query, and the memory item lifecycle on the
// derived brain object (agent_memory datasets). Mirrors chat_test.go's
// single-binary smoke style; handler-level validation detail lives in
// internal/agentlog and internal/agentmem unit tests.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

func TestE2E_AgentBinary(t *testing.T) {
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
		`{"name":"agent-binary"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	spaceBase := base + "/v1/spaces/" + sp.Id
	objBase := spaceBase + "/objects/" + obj.ObjectId

	// --- turns: append four, server stamps creator/createdAt ---------------
	for seq := range 4 {
		var res api.ModifyResult
		mustJSON(t, http.MethodPost, objBase+"/agent/turns",
			fmt.Sprintf(`{"seq":%d,"userName":"alice","userText":"msg %d",
				"think":"thinking about %d","replies":["reply %d"],
				"effects":["created Thing [x](any://s/o)"],
				"llm":{"stopReason":"done","inTokens":100,"outTokens":20,"model":"test"}}`,
				seq, seq, seq, seq),
			http.StatusCreated, &res)
		if len(res.RecordIds) != 1 || res.RecordIds[0] != fmt.Sprintf("%08d", seq) {
			t.Fatalf("turn %d: recordIds = %v, want [%08d]", seq, res.RecordIds, seq)
		}
	}

	turns := queryDataset(t, spaceBase, obj.ObjectId, "agent_turns", map[string]any{}, []string{"seq"})
	if len(turns) != 4 {
		t.Fatalf("turns = %d, want 4", len(turns))
	}
	for i, rec := range turns {
		if int(rec["seq"].(float64)) != i {
			t.Errorf("turns[%d].seq = %v", i, rec["seq"])
		}
		if rec["creator"] == nil || rec["creator"] == "" {
			t.Errorf("turns[%d].creator unstamped: %+v", i, rec)
		}
		if rec["createdAt"] == nil || rec["createdAt"].(float64) <= 0 {
			t.Errorf("turns[%d].createdAt unstamped: %+v", i, rec)
		}
	}

	// Duplicate seq → the upsert turns into a modify, which the
	// append-only handler rejects. Anything but 201 is fine; assert
	// the record did not change.
	resp, _ := doRequest(t, http.MethodPost, objBase+"/agent/turns",
		`{"seq":0,"userText":"overwrite attempt"}`)
	if resp.StatusCode == http.StatusCreated {
		t.Errorf("duplicate seq append: status = 201, want rejection")
	}
	turns = queryDataset(t, spaceBase, obj.ObjectId, "agent_turns",
		map[string]any{"seq": 0}, nil)
	if len(turns) != 1 || turns[0]["userText"] != "msg 0" {
		t.Errorf("turn 0 mutated by duplicate append: %+v", turns)
	}

	// Omitted seq → server-assigned: max(seq)+1 after the four appends.
	var assigned api.ModifyResult
	mustJSON(t, http.MethodPost, objBase+"/agent/turns",
		`{"userText":"no seq"}`, http.StatusCreated, &assigned)
	if len(assigned.RecordIds) != 1 || assigned.RecordIds[0] != "00000004" {
		t.Errorf("server-assigned seq: recordIds = %v, want [00000004]", assigned.RecordIds)
	}

	// Turn validation envelope: negative seq → 400 agent.seq_required.
	var env api.ErrorEnvelope
	mustJSON(t, http.MethodPost, objBase+"/agent/turns",
		`{"seq":-1,"userText":"bad seq"}`, http.StatusBadRequest, &env)
	if env.Error.Code != api.ErrAgentSeqRequired {
		t.Errorf("negative seq: code = %q, want %q", env.Error.Code, api.ErrAgentSeqRequired)
	}

	// --- chunks: create one covering turns 0..1, then drill down -----------
	var chunkRes api.ModifyResult
	mustJSON(t, http.MethodPost, objBase+"/agent/chunks",
		`{"seq":0,"summary":"alice sent two messages; agent replied",
		  "periodStart":1700000000,"periodEnd":1700003600,
		  "fromSeq":0,"toSeq":1,"turnsCovered":2}`,
		http.StatusCreated, &chunkRes)

	chunks := queryDataset(t, spaceBase, obj.ObjectId, "agent_chunks", map[string]any{}, []string{"seq"})
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	chunk := chunks[0]
	fromSeq := int(chunk["fromSeq"].(float64))
	toSeq := int(chunk["toSeq"].(float64))
	if fromSeq != 0 || toSeq != 1 {
		t.Fatalf("chunk pointers = %d..%d, want 0..1", fromSeq, toSeq)
	}

	// The layering contract: chunk pointers → indexed range query over
	// the raw turns it summarizes.
	covered := queryDataset(t, spaceBase, obj.ObjectId, "agent_turns",
		map[string]any{"seq": map[string]any{"$gte": fromSeq, "$lte": toSeq}},
		[]string{"seq"})
	if len(covered) != 2 {
		t.Fatalf("drill-down = %d turns, want 2", len(covered))
	}
	if covered[0]["userText"] != "msg 0" || covered[1]["userText"] != "msg 1" {
		t.Errorf("drill-down content: %v, %v", covered[0]["userText"], covered[1]["userText"])
	}

	// Chunk validation envelope: inverted range → 400 agent.chunk_invalid.
	mustJSON(t, http.MethodPost, objBase+"/agent/chunks",
		`{"seq":1,"summary":"s","periodStart":1,"periodEnd":2,"fromSeq":5,"toSeq":2}`,
		http.StatusBadRequest, &env)
	if env.Error.Code != api.ErrAgentChunkInvalid {
		t.Errorf("inverted range: code = %q, want %q", env.Error.Code, api.ErrAgentChunkInvalid)
	}

	// --- brain: deterministic id, stable across calls -----------------------
	var brain1, brain2 api.AgentBrainResponse
	mustJSON(t, http.MethodGet, spaceBase+"/agent/brain", "", http.StatusOK, &brain1)
	mustJSON(t, http.MethodGet, spaceBase+"/agent/brain", "", http.StatusOK, &brain2)
	if brain1.ObjectId == "" || brain1.ObjectId != brain2.ObjectId {
		t.Fatalf("brain id not deterministic: %q vs %q", brain1.ObjectId, brain2.ObjectId)
	}

	// --- memory: create with defaults, category filter, evolve, delete ------
	var memRes api.ModifyResult
	mustJSON(t, http.MethodPost, spaceBase+"/agent/memory",
		`{"category":"preference","context":"user prefers dark mode",
		  "body":"always pick the dark theme","tags":["ui","style"],
		  "entities":["dark mode"]}`,
		http.StatusCreated, &memRes)
	if len(memRes.RecordIds) != 1 || memRes.RecordIds[0] == "" {
		t.Fatalf("memory create: recordIds = %v", memRes.RecordIds)
	}
	itemId := memRes.RecordIds[0]

	mustJSON(t, http.MethodPost, spaceBase+"/agent/memory",
		`{"category":"lesson","context":"tests need the staging fixture"}`,
		http.StatusCreated, nil)

	// Server defaults landed (confidence/importance 5, salience 10,
	// accessCount 0, validFrom = createdAt).
	items := queryDataset(t, spaceBase, brain1.ObjectId, "agent_memory_items",
		map[string]any{"category": "preference"}, nil)
	if len(items) != 1 {
		t.Fatalf("category filter = %d items, want 1", len(items))
	}
	item := items[0]
	for field, want := range map[string]float64{
		"confidence": 5, "importance": 5, "salience": 10, "accessCount": 0,
	} {
		if got, _ := item[field].(float64); got != want {
			t.Errorf("default %s = %v, want %v", field, item[field], want)
		}
	}
	if vf, _ := item["validFrom"].(float64); vf <= 0 {
		t.Errorf("validFrom not defaulted: %v", item["validFrom"])
	}
	if item["creator"] == nil || item["creator"] == "" {
		t.Errorf("creator unstamped: %+v", item)
	}

	// Evolve: salience down, tags replaced; modifiedAt bumps.
	var evRes api.ModifyResult
	mustJSON(t, http.MethodPatch, spaceBase+"/agent/memory/"+itemId,
		`{"salience":3,"tags":["ui"]}`, http.StatusOK, &evRes)
	items = queryDataset(t, spaceBase, brain1.ObjectId, "agent_memory_items",
		map[string]any{"id": itemId}, nil)
	if len(items) != 1 {
		t.Fatalf("item lookup after evolve = %d", len(items))
	}
	evolved := items[0]
	if got, _ := evolved["salience"].(float64); got != 3 {
		t.Errorf("evolved salience = %v, want 3", evolved["salience"])
	}
	if ma, ca := evolved["modifiedAt"].(float64), evolved["createdAt"].(float64); ma < ca {
		t.Errorf("modifiedAt %v < createdAt %v", ma, ca)
	}

	// Evolve with no fields → 400.
	mustJSON(t, http.MethodPatch, spaceBase+"/agent/memory/"+itemId,
		`{}`, http.StatusBadRequest, &env)

	// Evolve unknown item → 404 agent.memory_not_found.
	mustJSON(t, http.MethodPatch, spaceBase+"/agent/memory/missing",
		`{"salience":1}`, http.StatusNotFound, &env)
	if env.Error.Code != api.ErrAgentMemoryNotFound {
		t.Errorf("missing evolve: code = %q, want %q", env.Error.Code, api.ErrAgentMemoryNotFound)
	}

	// Create without category → 400 agent.memory_invalid.
	mustJSON(t, http.MethodPost, spaceBase+"/agent/memory",
		`{"context":"no category"}`, http.StatusBadRequest, &env)
	if env.Error.Code != api.ErrAgentMemoryInvalid {
		t.Errorf("missing category: code = %q, want %q", env.Error.Code, api.ErrAgentMemoryInvalid)
	}

	// Delete, then verify it's gone (the lesson item remains).
	mustStatus(t, http.MethodDelete, spaceBase+"/agent/memory/"+itemId, "", http.StatusOK)
	items = queryDataset(t, spaceBase, brain1.ObjectId, "agent_memory_items", map[string]any{}, nil)
	if len(items) != 1 {
		t.Fatalf("items after delete = %d, want 1", len(items))
	}
	if items[0]["category"] != "lesson" {
		t.Errorf("surviving item category = %v, want lesson", items[0]["category"])
	}
}

// queryDataset reads records off a per-object dataset via POST /query
// — the canonical read path for all agent datasets (no bespoke read
// endpoints). Decodes each record to a generic map; numbers come back
// as float64.
func queryDataset(t *testing.T, spaceBase, objectId, dataset string, filter map[string]any, sort []string) []map[string]any {
	t.Helper()
	bodyMap := map[string]any{
		"objectId": objectId,
		"dataset":  dataset,
	}
	if len(filter) > 0 {
		bodyMap["filter"] = filter
	}
	if len(sort) > 0 {
		bodyMap["sort"] = sort
	}
	body, _ := json.Marshal(bodyMap)
	resp, raw := doRequest(t, http.MethodPost, spaceBase+"/query", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query %s: status=%d body=%s", dataset, resp.StatusCode, raw)
	}
	var qr struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(raw, &qr); err != nil {
		t.Fatalf("query %s: decode: %v body=%s", dataset, err, raw)
	}
	return qr.Records
}
