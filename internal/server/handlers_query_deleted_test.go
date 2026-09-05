package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/dataview"
)

// TestServer_Query_IncludeDeleted pins the tombstone-inclusive
// snapshot: with `includeDeleted` a deleted record comes back as a
// content-wiped row carrying `_deletedAt`, the live read stays
// tombstone-free, `sort: ["-id"]` finds the highest id ever used (the
// burned one — what a client-assigned-id writer needs), `total` counts
// tombstones, and the flag is refused where it cannot be honoured.
func TestServer_Query_IncludeDeleted(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)
	createView(t, e, spaceId, objectId, "v-1", defaultViewPayload)
	createView(t, e, spaceId, objectId, "v-2", defaultViewPayload)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/delete-records", fmt.Sprintf(`{
		"objectId": %q, "dataset": %q, "recordIds": ["v-2"]
	}`, objectId, dataview.DatasetViews))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	type row struct {
		Id        string          `json:"id"`
		Name      string          `json:"name"`
		DeletedAt json.RawMessage `json:"_deletedAt"`
	}
	query := func(extra string) (api.QueryResponse, []row) {
		t.Helper()
		body := fmt.Sprintf(`{"objectId": %q, "dataset": %q, "sort": ["-id"]%s}`, objectId, dataview.DatasetViews, extra)
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("query %s: %d %s", extra, rec.Code, rec.Body.String())
		}
		var res api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		rows := make([]row, 0, len(res.Records))
		for _, raw := range res.Records {
			var r row
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatalf("decode row %s: %v", raw, err)
			}
			rows = append(rows, r)
		}
		return res, rows
	}

	// Live read: the tombstone is invisible.
	if _, rows := query(""); len(rows) != 1 || rows[0].Id != "v-1" {
		t.Fatalf("live rows = %+v, want [v-1]", rows)
	}

	// Tombstone-inclusive read: content wiped, _deletedAt present,
	// id order puts the burned id first.
	res, rows := query(`, "includeDeleted": true, "includeTotal": true`)
	if len(rows) != 2 || rows[0].Id != "v-2" || rows[1].Id != "v-1" {
		t.Fatalf("rows = %+v, want [v-2 v-1]", rows)
	}
	if rows[0].Name != "" || len(rows[0].DeletedAt) == 0 || string(rows[0].DeletedAt) == "null" {
		t.Errorf("tombstone row = %+v, want content wiped + _deletedAt", rows[0])
	}
	if rows[1].Name == "" || len(rows[1].DeletedAt) != 0 {
		t.Errorf("live row = %+v, want content + no _deletedAt", rows[1])
	}
	if res.Total == nil || *res.Total != 2 || res.HasNext == nil || *res.HasNext {
		t.Errorf("total = %v hasNext = %v, want 2 / false", res.Total, res.HasNext)
	}

	// The max-id probe a client-assigned-id writer runs.
	res, rows = query(`, "includeDeleted": true, "includeTotal": true, "limit": 1`)
	if len(rows) != 1 || rows[0].Id != "v-2" {
		t.Errorf("max-id probe = %+v, want [v-2]", rows)
	}
	if res.Total == nil || *res.Total != 2 || res.HasNext == nil || !*res.HasNext {
		t.Errorf("paged total = %v hasNext = %v, want 2 / true", res.Total, res.HasNext)
	}

	// Refused on subscribe (the live window cannot carry tombstones)…
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query/subscribe",
		fmt.Sprintf(`{"objectId": %q, "dataset": %q, "includeDeleted": true}`, objectId, dataview.DatasetViews))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("subscribe with includeDeleted: %d %s, want 400", rec.Code, rec.Body.String())
	}
	// …and unknown on the cross-object query (deleted objects are
	// purged, not tombstoned — nothing to include).
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/query", `{"includeDeleted": true}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("objects/query with includeDeleted: %d %s, want 400", rec.Code, rec.Body.String())
	}
}
