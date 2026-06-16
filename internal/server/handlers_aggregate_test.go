package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// aggDo POSTs an aggregate body and decodes the response on 200.
func aggDo(t *testing.T, e http.Handler, path, body string) *api.AggregateResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, path, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s: status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	var out api.AggregateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode aggregate response: %v: %s", err, rec.Body.String())
	}
	return &out
}

// aggExpectError POSTs an aggregate body and asserts the canonical
// error envelope's status + code.
func aggExpectError(t *testing.T, e http.Handler, path, body string, wantStatus int, wantCode string) api.APIError {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, path, body)
	if rec.Code != wantStatus {
		t.Fatalf("POST %s: status=%d, want %d; body=%s", path, rec.Code, wantStatus, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v: %s", err, rec.Body.String())
	}
	if env.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q (message: %s)", env.Error.Code, wantCode, env.Error.Message)
	}
	return env.Error
}

// TestServer_Aggregate_ChatMessages drives the per-object dataset
// endpoint over chat_messages: group-by-creator, the `id` (not `_id`)
// group output key, and tombstone exclusion after a delete.
func TestServer_Aggregate_ChatMessages(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	first := chatSend(t, e, base, "first", "")
	chatSend(t, e, base, "second", "")
	chatSend(t, e, base, "third", "")

	aggPath := "/v1/spaces/" + spaceId + "/aggregate"
	groupByCreator := fmt.Sprintf(`{
		"objectId": %q, "dataset": "chat_messages",
		"pipeline": [{"$group": {"_id": "$creator", "n": {"$count": {}}}}]
	}`, objectId)

	out := aggDo(t, e, aggPath, groupByCreator)
	if len(out.Records) != 1 {
		t.Fatalf("got %d groups, want 1: %+v", len(out.Records), out.Records)
	}
	var group struct {
		Id     *string `json:"id"`
		OldId  *string `json:"_id"`
		N      int     `json:"n"`
	}
	if err := json.Unmarshal(out.Records[0], &group); err != nil {
		t.Fatalf("decode group: %v: %s", err, out.Records[0])
	}
	if group.OldId != nil {
		t.Errorf("group output carries _id — the output key must be id: %s", out.Records[0])
	}
	if group.Id == nil || *group.Id == "" {
		t.Errorf("group key id empty: %s", out.Records[0])
	}
	if group.N != 3 {
		t.Errorf("n = %d, want 3", group.N)
	}

	// Tombstone exclusion: delete one message, the count drops.
	rec := doJSON(t, e, http.MethodDelete, base+"/chat/messages/"+first.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE message: %d %s", rec.Code, rec.Body.String())
	}
	out = aggDo(t, e, aggPath, groupByCreator)
	if len(out.Records) != 1 {
		t.Fatalf("after delete: got %d groups, want 1", len(out.Records))
	}
	if err := json.Unmarshal(out.Records[0], &group); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	if group.N != 2 {
		t.Errorf("after delete: n = %d, want 2 (tombstone still aggregated)", group.N)
	}

	// A never-materialised dataset aggregates to an empty-but-present
	// records array (`"records": []`, not `{}` and not an error).
	rec = doJSON(t, e, http.MethodPost, aggPath, fmt.Sprintf(
		`{"objectId": %q, "dataset": "no-such-dataset", "pipeline": [{"$count": "n"}]}`, objectId))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty dataset: %d %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["records"]) != "[]" {
		t.Errorf("empty dataset records = %s, want []", raw["records"])
	}
}

// TestServer_Aggregate_Objects drives the objects-collection endpoint:
// $match prefix + $group over property values, plus explain.
func TestServer_Aggregate_Objects(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, yearProp := setupAggMovies(t, e, map[string]int{
		"Brazil": 1985, "Aliens": 1986, "Platoon": 1986,
	})
	aggPath := "/v1/spaces/" + spaceId + "/objects/aggregate"
	yearPath := typeId + "." + yearProp

	body := fmt.Sprintf(`{"pipeline": [
		{"$match": {%q: {"$gte": 1980}}},
		{"$group": {"_id": "$%s", "n": {"$count": {}}}},
		{"$sort": {"id": 1}}
	]}`, yearPath, yearPath)

	out := aggDo(t, e, aggPath, body)
	if len(out.Records) != 2 {
		t.Fatalf("got %d groups, want 2: %v", len(out.Records), out.Records)
	}
	var groups []struct {
		Id float64 `json:"id"`
		N  int     `json:"n"`
	}
	for _, r := range out.Records {
		var g struct {
			Id float64 `json:"id"`
			N  int     `json:"n"`
		}
		if err := json.Unmarshal(r, &g); err != nil {
			t.Fatalf("decode group: %v: %s", err, r)
		}
		groups = append(groups, g)
	}
	if groups[0].Id != 1985 || groups[0].N != 1 {
		t.Errorf("group[0] = %+v, want {1985 1}", groups[0])
	}
	if groups[1].Id != 1986 || groups[1].N != 2 {
		t.Errorf("group[1] = %+v, want {1986 2}", groups[1])
	}

	// Explain returns a plan instead of records.
	explainBody := fmt.Sprintf(`{"explain": true, "pipeline": [
		{"$match": {%q: {"$gte": 1980}}},
		{"$group": {"_id": "$%s", "n": {"$count": {}}}}
	]}`, yearPath, yearPath)
	out = aggDo(t, e, aggPath, explainBody)
	if out.Plan == nil || *out.Plan == "" {
		t.Fatalf("explain: empty plan: %+v", out)
	}
	if len(out.Records) != 0 {
		t.Errorf("explain: records must be empty, got %d", len(out.Records))
	}
}

// TestServer_Aggregate_Errors pins the 4xx surface: missing/invalid
// pipeline, missing dataset, bad stages, prefix-only $text, limit
// overruns, unknown space.
func TestServer_Aggregate_Errors(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, yearProp := setupAggMovies(t, e, map[string]int{
		"Brazil": 1985, "Aliens": 1986,
	})
	objPath := "/v1/spaces/" + spaceId + "/objects/aggregate"
	dsPath := "/v1/spaces/" + spaceId + "/aggregate"
	yearPath := typeId + "." + yearProp

	// Missing pipeline.
	aggExpectError(t, e, objPath, `{}`, http.StatusBadRequest, "request.missing_field")
	// Pipeline must be an array.
	aggExpectError(t, e, objPath, `{"pipeline": {"$match": {}}}`, http.StatusBadRequest, "request.schema")
	// Per-object endpoint requires objectId + dataset.
	aggExpectError(t, e, dsPath, `{"pipeline": []}`, http.StatusBadRequest, "request.missing_field")
	aggExpectError(t, e, dsPath, `{"objectId": "x", "pipeline": []}`, http.StatusBadRequest, "request.missing_field")
	// Unknown stage.
	aggExpectError(t, e, objPath, `{"pipeline": [{"$bogus": {}}]}`, http.StatusBadRequest, "aggregate.bad_pipeline")
	// $text outside the pushdown prefix.
	aggExpectError(t, e, objPath,
		`{"pipeline": [{"$group": {"_id": "$a"}}, {"$match": {"$text": "x"}}]}`,
		http.StatusBadRequest, "aggregate.bad_pipeline")
	// Group-limit overrun (2 distinct years, limit 1) — surfaces
	// mid-iteration, still a 400 with the limit kind in details.
	apiErr := aggExpectError(t, e, objPath, fmt.Sprintf(
		`{"groupLimit": 1, "pipeline": [{"$group": {"_id": "$%s", "n": {"$count": {}}}}]}`, yearPath),
		http.StatusBadRequest, "aggregate.limit_exceeded")
	if apiErr.Details["limit"] != "group" {
		t.Errorf("details.limit = %v, want group", apiErr.Details["limit"])
	}
	// Unknown space — 500 internal for now: resolveSpace/spaceError
	// can't classify "unknown space" until the SDK exports a sentinel
	// (same behavior as every other per-space endpoint).
	aggExpectError(t, e, "/v1/spaces/nope/objects/aggregate", `{"pipeline": []}`,
		http.StatusInternalServerError, "internal")
}

// setupAggMovies creates a space + Movie type with a Year property and
// seeds one object per (title, year). Returns (spaceId, typeId,
// yearPropId).
func setupAggMovies(t *testing.T, e http.Handler, movies map[string]int) (string, string, string) {
	t.Helper()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"AggTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Movie"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/types/%s/properties", sp.Id, typeResp.TypeId),
		`{"name":"Year","kind":"number","xKey":"year"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add property: %d %s", rec.Code, rec.Body.String())
	}
	var propResp api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &propResp); err != nil {
		t.Fatalf("decode prop: %v", err)
	}

	for title, year := range movies {
		rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
			fmt.Sprintf(`{"types":[%q]}`, typeResp.TypeId))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create object %s: %d %s", title, rec.Code, rec.Body.String())
		}
		var obj api.ObjectsCreateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
			t.Fatalf("decode object: %v", err)
		}
		rec = doJSON(t, e, http.MethodPost,
			fmt.Sprintf("/v1/spaces/%s/properties/%s/set/%s", sp.Id, obj.ObjectId, typeResp.TypeId),
			fmt.Sprintf(`{"patch":{%q:%d}}`, propResp.PropId, year))
		if rec.Code != http.StatusOK {
			t.Fatalf("set base %s: %d %s", title, rec.Code, rec.Body.String())
		}
	}
	return sp.Id, typeResp.TypeId, propResp.PropId
}
