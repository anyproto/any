package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// objectRowOf reads an object's row through GET …/objects/:objectId.
func objectRowOf(t *testing.T, e http.Handler, spaceId, objectId string) map[string]any {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+objectId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET object %s: %d %s", objectId, rec.Code, rec.Body.String())
	}
	var resp api.ObjectGetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(resp.Record, &row); err != nil {
		t.Fatal(err)
	}
	return row
}

// createdInOf reads the row's meta.createdIn; "" when absent.
func createdInOf(t *testing.T, e http.Handler, spaceId, objectId string) string {
	t.Helper()
	bag, _ := objectRowOf(t, e, spaceId, objectId)["meta"].(map[string]any)
	v, _ := bag["createdIn"].(string)
	return v
}

// TestServer_CreatedIn pins createdIn over HTTP: set by the create alone
// into the row's creation bag (meta.createdIn), read back on the row,
// filterable, carried by the subscribe frames, never rewritten, not
// copied by `from`, and the typed refusals.
func TestServer_CreatedIn(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	spaceId, typeId, note := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId

	// A live stream over the filter first, so the create's own frame is
	// observed.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	frames := make(chan client.SSEFrame, 64)
	go func() {
		_ = cl.StreamObjectsQuerySubscribe(ctx, spaceId, []byte(`{"filter":{"meta.createdIn":"`+note+`"}}`), func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-ctx.Done():
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
	var snapData api.QuerySubscribeSnapshot
	if err := json.Unmarshal(snap.Data, &snapData); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snapData.Records) != 0 {
		t.Fatalf("snapshot holds %d records before anything was created in the note", len(snapData.Records))
	}

	task := mustCreateObject(t, e, spaceId,
		`{"type":"`+typeId+`","createdIn":"`+note+`","initialProperties":{"any":{"name":"Buy milk"}}}`)
	row := objectRowOf(t, e, spaceId, task)
	if got := createdInOf(t, e, spaceId, task); got != note {
		t.Fatalf("row meta.createdIn = %q, want %s (row %v)", got, note, row)
	}
	if anyNs, _ := row["any"].(map[string]any); anyNs["meta"] != nil || anyNs["createdIn"] != nil {
		t.Errorf("the bag landed under any.*: %v", anyNs)
	}
	if got := createdInOf(t, e, spaceId, note); got != "" {
		t.Errorf("the host row carries createdIn %q", got)
	}

	// The subscribe frame carries the stamped row.
	ev := awaitWindowedEvent(t, frames, task, windowedAdded)
	var seen bool
	for _, r := range ev.Added {
		if r.Id != task {
			continue
		}
		seen = true
		var doc map[string]any
		if err := json.Unmarshal(r.Doc, &doc); err != nil {
			t.Fatalf("decode added doc: %v", err)
		}
		if bag, _ := doc["meta"].(map[string]any); bag["createdIn"] != note {
			t.Errorf("added doc meta = %v, want createdIn %s", doc["meta"], note)
		}
	}
	if !seen {
		t.Fatalf("no added record for %s in %+v", task, ev)
	}

	// A fresh stream's snapshot carries the stamped row.
	frames2 := make(chan client.SSEFrame, 64)
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go func() {
		_ = cl.StreamObjectsQuerySubscribe(ctx2, spaceId, []byte(`{"filter":{"meta.createdIn":"`+note+`"}}`), func(f client.SSEFrame) error {
			select {
			case frames2 <- f:
			case <-ctx2.Done():
			}
			return nil
		})
	}()
	if got := waitFrame(t, frames2, 5*time.Second); got.Event != "ready" {
		t.Fatalf("second stream: first frame = %q, want ready", got.Event)
	}
	snap2 := waitFrame(t, frames2, 5*time.Second)
	if snap2.Event != "snapshot" {
		t.Fatalf("second stream: second frame = %q, want snapshot", snap2.Event)
	}
	if err := json.Unmarshal(snap2.Data, &snapData); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snapData.Records) != 1 || !strings.Contains(string(snapData.Records[0]), `"createdIn":"`+note+`"`) {
		t.Errorf("snapshot after the create = %s, want the stamped task", snap2.Data)
	}
	cancel2()

	// The filter lists what was created in the note.
	rec := doJSON(t, e, http.MethodPost, base+"/objects/query", `{"filter":{"meta.createdIn":"`+note+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	var q api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Records) != 1 || !strings.Contains(string(q.Records[0]), `"id":"`+task+`"`) {
		t.Fatalf("filter on createdIn = %s, want the task alone", rec.Body.String())
	}
	// Served by the index the server declares, not a scan.
	rec = doJSON(t, e, http.MethodPost, base+"/objects/aggregate",
		`{"explain":true,"pipeline":[{"$match":{"meta.createdIn":"`+note+`"}}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "idx_meta_created_in") {
		t.Errorf("explain of the createdIn filter: %d %s, want idx_meta_created_in", rec.Code, rec.Body.String())
	}

	// Listed by the `any` type as a derived object, like the other
	// stamps.
	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/types/any/properties", &props)
	var listed bool
	for _, p := range props.Properties {
		if p.Id == "meta" {
			listed = true
			if p.Kind != api.PropertyKindObject || p.Scope != "derived" {
				t.Errorf("any.meta listed as %+v, want a derived object", p)
			}
		}
	}
	if !listed {
		t.Errorf("any's property listing lacks meta: %+v", props.Properties)
	}

	// No later write moves it: a raw $set or $unset of the bag or of a
	// key inside it, or a property write to any.meta, is refused and
	// the row keeps the stamp.
	other := mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`"}`)
	for _, ops := range []string{
		`{"type":"$set","path":"","value":{"meta":{"createdIn":"` + other + `"}}}`,
		`{"type":"$set","path":"","value":{"meta.createdIn":"` + other + `"}}`,
		`{"type":"$set","path":"meta","value":{"createdIn":"` + other + `"}}`,
		`{"type":"$set","path":"meta.createdIn","value":"` + other + `"}`,
		`{"type":"$unset","path":"meta"}`,
		`{"type":"$unset","path":"meta.createdIn"}`,
	} {
		rec = doJSON(t, e, http.MethodPost, base+"/modify",
			`{"objectId":"`+task+`","dataset":"objects","records":[{"id":"`+task+`","ops":[`+ops+`]}]}`)
		assertStatusCode(t, rec, http.StatusBadRequest, "dataset.validation")
	}
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+task+"/set/any", `{"patch":{"meta":{"createdIn":"`+other+`"}}}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.validation")
	if got := createdInOf(t, e, spaceId, task); got != note {
		t.Fatalf("meta.createdIn after the refused writes = %q, want %s", got, note)
	}
	// Nor is it a value a create may seed under any.*.
	rec = doJSON(t, e, http.MethodPost, base+"/objects",
		`{"type":"`+typeId+`","initialProperties":{"any":{"meta":{"createdIn":"`+note+`"}}}}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.validation")

	// `from` does not copy the source's stamp; the request's own applies.
	dup := mustCreateObject(t, e, spaceId, `{"from":"`+task+`"}`)
	if bag := objectRowOf(t, e, spaceId, dup)["meta"]; bag != nil {
		t.Errorf("from copied the creation bag %v", bag)
	}
	dup2 := mustCreateObject(t, e, spaceId, `{"from":"`+task+`","createdIn":"`+other+`"}`)
	if got := createdInOf(t, e, spaceId, dup2); got != other {
		t.Errorf("copy made in %s has createdIn %q", other, got)
	}

	// null reads as absent; a definition's row is a host like any object.
	plain := mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`","createdIn":null}`)
	if got := createdInOf(t, e, spaceId, plain); got != "" {
		t.Errorf("createdIn: null stamped %q", got)
	}
	inType := mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`","createdIn":"`+typeId+`"}`)
	if got := createdInOf(t, e, spaceId, inType); got != typeId {
		t.Errorf("created in a type's row: createdIn = %q, want %s", got, typeId)
	}
	coll := mustCreateCollection(t, e, spaceId, `{"name":"Inbox","xKey":"inbox"}`)
	inColl := mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`","createdIn":"`+coll+`"}`)
	if got := createdInOf(t, e, spaceId, inColl); got != coll {
		t.Errorf("created in a collection's row: createdIn = %q, want %s", got, coll)
	}

	// Shape and id refusals, each with nothing minted.
	gone := mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`"}`)
	if rec := doJSON(t, e, http.MethodDelete, base+"/objects/"+gone, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	foreignSpace := createSpaceInfo(t, e, "Elsewhere")
	foreign := mustCreateObject(t, e, foreignSpace.Id, `{"type":"page"}`)
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"number", `{"type":"` + typeId + `","createdIn":7}`, "request.schema", http.StatusBadRequest},
		{"array", `{"type":"` + typeId + `","createdIn":["x"]}`, "request.schema", http.StatusBadRequest},
		{"empty", `{"type":"` + typeId + `","createdIn":""}`, "request.schema", http.StatusBadRequest},
		{"serialized nil", `{"type":"` + typeId + `","createdIn":"undefined"}`, "object.id_required", http.StatusBadRequest},
		{"unknown", `{"type":"` + typeId + `","createdIn":"bafyunknown"}`, "object.not_found", http.StatusNotFound},
		{"another space's", `{"type":"` + typeId + `","createdIn":"` + foreign + `"}`, "object.not_found", http.StatusNotFound},
		{"deleted", `{"type":"` + typeId + `","createdIn":"` + gone + `"}`, "object.deleted", http.StatusGone},
	} {
		rec := doJSON(t, e, http.MethodPost, base+"/objects", tc.body)
		assertStatusCode(t, rec, tc.status, tc.code)
		if tc.status != http.StatusBadRequest || tc.code != "request.schema" {
			var env api.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if tc.code == "object.not_found" || tc.code == "object.deleted" {
				if got, _ := env.Error.Details["createdIn"].(string); got == "" {
					t.Errorf("%s: details lack createdIn: %v", tc.name, env.Error.Details)
				}
			}
		}
	}
	rec = doJSON(t, e, http.MethodPost, base+"/objects/query", `{"filter":{"any.type":"`+typeId+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	// note, task, other, dup, dup2, plain, inType, inColl — no row from
	// a refused create.
	if len(q.Records) != 8 {
		t.Errorf("rows of the type after the refusals = %d, want 8", len(q.Records))
	}
}
