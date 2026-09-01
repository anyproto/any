package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_Local_Projection: /v1/local/query takes the same
// projection grammar as the dataset query endpoints, so a client does
// not have to remember which surface accepts it. A local record has no
// `_ver` and no delivery counters, so only the user fields it wrote
// are in play — `$project` inside /v1/local/aggregate is the pipeline
// equivalent.
func TestServer_Local_Projection(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	localEnsure(t, e, accColl, http.StatusCreated)

	localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/insert",
		`{"coll":`+accColl+`,"docs":[{"id":"a","title":"one","body":"long","n":1}]}`, http.StatusOK)

	read := func(body string) map[string]any {
		t.Helper()
		res := localDo[api.QueryResponse](t, e, http.MethodPost, "/v1/local/query", body, http.StatusOK)
		if len(res.Records) != 1 {
			t.Fatalf("want 1 record, got %d", len(res.Records))
		}
		var m map[string]any
		if err := json.Unmarshal(res.Records[0], &m); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		return m
	}

	// Include mode: id rides along, everything unlisted is gone.
	got := read(`{"coll":` + accColl + `,"projection":{"title":1}}`)
	if got["id"] != "a" || got["title"] != "one" {
		t.Errorf("include mode lost a projected field: %v", got)
	}
	for _, gone := range []string{"body", "n"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%q should not be projected: %v", gone, keys(got))
		}
	}

	// Exclude mode.
	got = read(`{"coll":` + accColl + `,"projection":{"body":-1}}`)
	if _, ok := got["body"]; ok {
		t.Errorf("body should be excluded: %v", keys(got))
	}
	if got["title"] != "one" || got["n"] == nil {
		t.Errorf("exclude mode should keep the rest: %v", got)
	}

	// No projection is unchanged.
	got = read(`{"coll":` + accColl + `}`)
	for _, want := range []string{"id", "title", "body", "n"} {
		if _, ok := got[want]; !ok {
			t.Errorf("unprojected read missing %q: %v", want, keys(got))
		}
	}

	// Same validation surface as the dataset endpoints.
	localExpectError(t, e, http.MethodPost, "/v1/local/query",
		`{"coll":`+accColl+`,"projection":{"id":-1}}`, http.StatusBadRequest, "request.invalid_field")
	localExpectError(t, e, http.MethodPost, "/v1/local/query",
		`{"coll":`+accColl+`,"projection":{"title":"yes"}}`, http.StatusBadRequest, "request.invalid_field")
}
