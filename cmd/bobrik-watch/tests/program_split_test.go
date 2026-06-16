//go:build integration

// Integration test: pin the server-side storage contract for SPLIT tool docs —
// program_description holds the description body, program_methods holds one
// record per method ({name, kind, text, pos}, id = bareName), and the
// program.any_tool boolean is patchable via properties/:objId/set/program.
// The split/write logic itself lives in the writers (bobrik-watch sync.go,
// anyPrograms.saveProgram) and is covered by toolmd_test.go (Go splitter unit
// tests) and tests/js/programs_test.js (JS writer); this file proves the
// server primitives those writers compose behave as assumed.
//
// Run against a live server: see program_roundtrip_test.go.
package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// patchProgramProps POSTs {patch} to properties/:objId/set/program.
func patchProgramProps(t *testing.T, spaceID, objectID string, patch map[string]any) {
	t.Helper()
	st, body := doJSON(t, http.MethodPost,
		"/v1/spaces/"+spaceID+"/properties/"+objectID+"/set/program",
		map[string]any{"patch": patch})
	if st != http.StatusOK && st != http.StatusNoContent {
		t.Fatalf("patch program props: %d %s", st, body)
	}
}

// programAnyTool reads program.any_tool off the object's property record.
func programAnyTool(t *testing.T, spaceID, objectID string) bool {
	t.Helper()
	st, body := doJSON(t, http.MethodGet, "/v1/spaces/"+spaceID+"/properties/"+objectID, nil)
	if st != http.StatusOK {
		t.Fatalf("get properties: %d %s", st, body)
	}
	var out struct {
		Record struct {
			Program struct {
				AnyTool bool `json:"any_tool"`
			} `json:"program"`
		} `json:"record"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode properties: %v", err)
	}
	return out.Record.Program.AnyTool
}

// methodRecord mirrors the program_methods storage shape.
type methodRecord struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	Pos  int    `json:"pos"`
}

func writeMethodRecord(t *testing.T, spaceID, objectID string, m methodRecord) {
	t.Helper()
	st, body := doJSON(t, http.MethodPost, "/v1/spaces/"+spaceID+"/modify", map[string]any{
		"objectId": objectID,
		"dataset":  "program_methods",
		"records": []any{map[string]any{
			"id":     m.Id,
			"upsert": true,
			"ops": []any{map[string]any{"type": "$set", "path": "", "value": map[string]any{
				"name": m.Name, "kind": m.Kind, "text": m.Text, "pos": m.Pos,
			}}},
		}},
	})
	if st != http.StatusOK {
		t.Fatalf("modify program_methods: %d %s", st, body)
	}
}

func queryMethodRecords(t *testing.T, spaceID, objectID string) []methodRecord {
	t.Helper()
	st, body := doJSON(t, http.MethodPost, "/v1/spaces/"+spaceID+"/query", map[string]any{
		"objectId": objectID,
		"dataset":  "program_methods",
		"sort":     []string{"pos"},
	})
	if st != http.StatusOK {
		t.Fatalf("query program_methods: %d %s", st, body)
	}
	var out struct {
		Records []methodRecord `json:"records"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode program_methods: %v", err)
	}
	return out.Records
}

func deleteMethodRecords(t *testing.T, spaceID, objectID string, ids []string) {
	t.Helper()
	st, body := doJSON(t, http.MethodPost, "/v1/spaces/"+spaceID+"/delete-records", map[string]any{
		"objectId": objectID, "dataset": "program_methods", "recordIds": ids,
	})
	if st != http.StatusOK && st != http.StatusNoContent {
		t.Fatalf("delete-records: %d %s", st, body)
	}
}

// TestProgramAnyToolProperty: any_tool is declared on the builtin program
// type, settable at create (initialProperties) and patchable afterwards.
func TestProgramAnyToolProperty(t *testing.T) {
	spaceID := ensureSpace(t)
	name := fmt.Sprintf("split_at_%d", time.Now().UnixNano())

	// Create WITH any_tool: true in initialProperties.
	st, body := doJSON(t, http.MethodPost, "/v1/spaces/"+spaceID+"/objects", map[string]any{
		"types": []string{"program"},
		"initialProperties": map[string]any{
			"any":     map[string]any{"name": name},
			"program": map[string]any{"name": name, "version": "v1", "any_tool": true},
		},
	})
	if st != http.StatusCreated {
		t.Fatalf("create object with any_tool: %d %s", st, body)
	}
	var created struct {
		ObjectID string `json:"objectId"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created object: %v", err)
	}
	if !programAnyTool(t, spaceID, created.ObjectID) {
		t.Errorf("any_tool not true after create with initialProperties")
	}

	// Patch it off, then back on.
	patchProgramProps(t, spaceID, created.ObjectID, map[string]any{"any_tool": false})
	if programAnyTool(t, spaceID, created.ObjectID) {
		t.Errorf("any_tool still true after patch to false")
	}
	patchProgramProps(t, spaceID, created.ObjectID, map[string]any{"any_tool": true})
	if !programAnyTool(t, spaceID, created.ObjectID) {
		t.Errorf("any_tool not true after patch to true")
	}

	// Object created WITHOUT any_tool reads as false.
	plain := createProgramObject(t, spaceID, name+"_plain")
	if programAnyTool(t, spaceID, plain) {
		t.Errorf("any_tool true on object created without it")
	}
}

// TestProgramMethodsDataset: per-method records round-trip with their fields,
// come back ordered by pos, and stale ones delete cleanly (the reconcile the
// writers perform when a method is removed from the docs).
func TestProgramMethodsDataset(t *testing.T) {
	spaceID := ensureSpace(t)
	obj := createProgramObject(t, spaceID, fmt.Sprintf("split_m_%d", time.Now().UnixNano()))

	want := []methodRecord{
		{Id: "alpha", Name: "alpha(x)", Kind: "getter", Text: "**Input:**\n- `x` (string)", Pos: 0},
		{Id: "beta", Name: "beta(y)", Kind: "mutator", Text: "writes y", Pos: 1},
		{Id: "gamma", Name: "gamma()", Kind: "setup", Text: "", Pos: 2},
	}
	for _, m := range want {
		writeMethodRecord(t, spaceID, obj, m)
	}

	got := queryMethodRecords(t, spaceID, obj)
	if len(got) != 3 {
		t.Fatalf("got %d method records, want 3: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Reconcile: docs rewritten without beta → beta's record is deleted.
	deleteMethodRecords(t, spaceID, obj, []string{"beta"})
	got = queryMethodRecords(t, spaceID, obj)
	if len(got) != 2 || got[0].Id != "alpha" || got[1].Id != "gamma" {
		ids := make([]string, len(got))
		for i, r := range got {
			ids[i] = r.Id
		}
		t.Fatalf("after delete, ids = %v, want [alpha gamma]", ids)
	}
}
