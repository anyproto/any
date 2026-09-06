package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/dataview"
)

// TestServer_Query_IncludeDeletedProjection pins where the two query
// knobs meet: a tombstone is `{id, _deletedAt, _ver, _addSeq}` with the
// content wiped, and the projection shapes it like any other row. What
// must survive is `_deletedAt` — a client that cannot tell a tombstone
// from a live record would treat the deletion as an empty record.
func TestServer_Query_IncludeDeletedProjection(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)
	createView(t, e, spaceId, objectId, "p-1", defaultViewPayload)
	createView(t, e, spaceId, objectId, "p-2", defaultViewPayload)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/delete-records", fmt.Sprintf(`{
		"objectId": %q, "dataset": %q, "recordIds": ["p-2"]
	}`, objectId, dataview.DatasetViews))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	body := fmt.Sprintf(`{"objectId": %q, "dataset": %q, "sort": ["-id"],
		"includeDeleted": true, "projection": {"name": 1}}`, objectId, dataview.DatasetViews)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	var res api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("want the live row and the tombstone, got %d", len(res.Records))
	}

	byId := map[string]map[string]any{}
	for _, raw := range res.Records {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode row %s: %v", raw, err)
		}
		id, _ := m["id"].(string)
		byId[id] = m
	}

	tomb, ok := byId["p-2"]
	if !ok {
		t.Fatalf("tombstone missing: %v", byId)
	}
	if _, ok := tomb["_deletedAt"]; !ok {
		t.Errorf("a projected tombstone must stay recognisable as one: %v", keys(tomb))
	}
	if _, ok := tomb["name"]; ok {
		t.Errorf("a tombstone's content is wiped, so nothing should be projected from it: %v", tomb)
	}
	if _, ok := tomb["_addSeq"]; ok {
		t.Errorf("the delivery counter still drops under a projection: %v", keys(tomb))
	}

	live, ok := byId["p-1"]
	if !ok {
		t.Fatalf("live row missing: %v", byId)
	}
	if live["name"] == nil {
		t.Errorf("the projected field should be present on the live row: %v", live)
	}
	if _, ok := live["_deletedAt"]; ok {
		t.Errorf("a live row carries no _deletedAt: %v", keys(live))
	}
}
