package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/agentdebug"
	"github.com/anyproto/any/internal/api"
)

// TestServer_AgentDebugLog_Dataset exercises the agent_debug_log built-in
// type the same way the toolcaller's debug collector does: create one object
// (no markdown body), then upsert an ordered array of structured entry records
// into its `agent_debug_log` dataset via POST /modify, and read them back
// sorted by `seq` via POST /query. Confirms nested values (rawArgs object,
// cells array, response object) round-trip through the DefaultHandler.
func TestServer_AgentDebugLog_Dataset(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// Space.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"DebugTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	// The built-in type must be discoverable in the space's type list (so the
	// runtime's createObject("agent_debug_log", ...) resolves it).
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list types: %d %s", rec.Code, rec.Body.String())
	}
	var types api.TypesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &types); err != nil {
		t.Fatalf("decode types: %v", err)
	}
	var sawType bool
	for _, ti := range types.Types {
		if ti.Id == agentdebug.TypeId {
			sawType = true
			break
		}
	}
	if !sawType {
		t.Fatalf("built-in type %q not in type list: %+v", agentdebug.TypeId, types.Types)
	}

	// Object bound to the built-in type, no markdown body — the dataset is the
	// content.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"types":["`+agentdebug.TypeId+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}

	modifyURL := "/v1/spaces/" + sp.Id + "/modify"
	setRecord := func(recordID string, fields map[string]any) {
		t.Helper()
		ops := make([]map[string]any, 0, len(fields))
		for k, v := range fields {
			ops = append(ops, map[string]any{"type": "$set", "path": k, "value": v})
		}
		body, _ := json.Marshal(map[string]any{
			"objectId": obj.ObjectId,
			"dataset":  agentdebug.Dataset,
			"records":  []map[string]any{{"id": recordID, "upsert": true, "ops": ops}},
		})
		r := doJSON(t, e, http.MethodPost, modifyURL, string(body))
		if r.Code != http.StatusOK {
			t.Fatalf("modify %s: %d %s", recordID, r.Code, r.Body.String())
		}
	}

	// boot entry — note the nested rawArgs object.
	setRecord("000000_boot", map[string]any{
		"seq":     0,
		"kind":    "boot",
		"prompt":  "take a look at digest page content",
		"build":   "chat-scoping@v2",
		"chatId":  "bafychat",
		"rawArgs": map[string]any{"text": "hi", "apiKey": "(redacted)", "verbose": false},
	})
	// system_prompt entry.
	setRecord("000001_system_prompt", map[string]any{
		"seq":   1,
		"kind":  "system_prompt",
		"chars": 28367,
		"text":  "You are an object-first agent...",
	})
	// turn entry — extracted scalars + nested cells array.
	setRecord("000002_turn", map[string]any{
		"seq":        2,
		"kind":       "turn",
		"n":          1,
		"stopReason": "tool_use",
		"durationMs": 2476,
		"inTokens":   3106,
		"outTokens":  106,
		"cells": []map[string]any{
			{"code": "anyHelper.getTypes()", "result": "[...]", "isError": false, "executed": true},
		},
	})
	// done entry.
	setRecord("000003_done", map[string]any{
		"seq":       3,
		"kind":      "done",
		"status":    "end_turn",
		"turns":     1,
		"totalMs":   2900,
		"finalText": "done.",
	})

	// Read back sorted by seq.
	queryBody, _ := json.Marshal(map[string]any{
		"objectId": obj.ObjectId,
		"dataset":  agentdebug.Dataset,
		"sort":     []string{"seq"},
	})
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/query", string(queryBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("query dataset: %d %s", rec.Code, rec.Body.String())
	}
	var qr struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(qr.Records) != 4 {
		t.Fatalf("query returned %d records, want 4", len(qr.Records))
	}

	// Decode each record; verify seq order, kind, and nested-value fidelity.
	type entry struct {
		Seq       int              `json:"seq"`
		Kind      string           `json:"kind"`
		RawArgs   map[string]any   `json:"rawArgs"`
		Cells     []map[string]any `json:"cells"`
		FinalText string           `json:"finalText"`
	}
	entries := make([]entry, 0, len(qr.Records))
	for _, raw := range qr.Records {
		var en entry
		if err := json.Unmarshal(raw, &en); err != nil {
			t.Fatalf("decode record: %v: %s", err, string(raw))
		}
		entries = append(entries, en)
	}

	wantKinds := []string{"boot", "system_prompt", "turn", "done"}
	for i, en := range entries {
		if en.Seq != i {
			t.Errorf("record %d seq = %d, want %d", i, en.Seq, i)
		}
		if en.Kind != wantKinds[i] {
			t.Errorf("record %d kind = %q, want %q", i, en.Kind, wantKinds[i])
		}
	}

	// boot.rawArgs nested object survived.
	if got := entries[0].RawArgs["apiKey"]; got != "(redacted)" {
		t.Errorf("boot.rawArgs.apiKey = %v, want (redacted)", got)
	}
	// turn.cells nested array survived.
	if len(entries[2].Cells) != 1 || entries[2].Cells[0]["code"] != "anyHelper.getTypes()" {
		t.Errorf("turn.cells round-trip failed: %+v", entries[2].Cells)
	}
	// done.finalText.
	if entries[3].FinalText != "done." {
		t.Errorf("done.finalText = %q, want %q", entries[3].FinalText, "done.")
	}

	// Upsert fidelity: re-set one field on the turn record and confirm the
	// others are untouched (atomic per-path $set).
	setRecord("000002_turn", map[string]any{"stopReason": "end_turn"})
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/query", string(queryBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-query: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode re-query: %v", err)
	}
	if len(qr.Records) != 4 {
		t.Fatalf("post-upsert query returned %d records, want 4 (upsert created a dup?)", len(qr.Records))
	}
}
