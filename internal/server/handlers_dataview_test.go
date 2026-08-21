package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/dataview"
)

// viewRecord is the test-local decode target for a data_views record
// read back through /query. `query` / `layoutSettings` / `localSettings`
// stay raw — the point is that the server round-trips them verbatim.
type viewRecord struct {
	Id             string          `json:"id"`
	Name           string          `json:"name"`
	Icon           string          `json:"icon"`
	Pos            string          `json:"pos"`
	Layout         string          `json:"layout"`
	Query          json.RawMessage `json:"query"`
	LayoutSettings json.RawMessage `json:"layoutSettings"`
	LocalSettings  json.RawMessage `json:"localSettings"`
	Creator        string          `json:"creator"`
	// The generic schema handler stamps times as JSON floats
	// (NewNumberFloat64), so an int64 target fails to decode.
	CreatedAt  float64 `json:"createdAt"`
	ModifiedAt float64 `json:"modifiedAt"`
}

// setupViewFixture creates a space and a host object for saved views.
// The data_view type is a registered built-in, so nothing has to be
// created — attaching it is the only binding step, and a fresh object
// exercises the attach endpoint the same way a client would on an
// existing object.
func setupViewFixture(t *testing.T, e http.Handler) (spaceId, objectId string) {
	t.Helper()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"ViewTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/properties/%s/attach/%s", sp.Id, obj.ObjectId, dataview.TypeId), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("attach data_view: %d %s", rec.Code, rec.Body.String())
	}
	return sp.Id, obj.ObjectId
}

// listViews reads every view record on the object.
func listViews(t *testing.T, e http.Handler, spaceId, objectId string) []viewRecord {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId,
		"dataset":  dataview.Dataset,
		"sort":     []string{"pos"},
	})
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("query views: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	out := make([]viewRecord, 0, len(resp.Records))
	for _, raw := range resp.Records {
		var v viewRecord
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("decode view record: %v\nraw=%s", err, raw)
		}
		out = append(out, v)
	}
	return out
}

func getView(t *testing.T, e http.Handler, spaceId, objectId, id string) viewRecord {
	t.Helper()
	for _, v := range listViews(t, e, spaceId, objectId) {
		if v.Id == id {
			return v
		}
	}
	t.Fatalf("view %q not found", id)
	return viewRecord{}
}

// createView writes one view record with a client-supplied id.
func createView(t *testing.T, e http.Handler, spaceId, objectId, id, payload string) api.ModifyResult {
	t.Helper()
	body := fmt.Sprintf(`{
		"objectId": %q, "dataset": %q,
		"records": [{"id": %q, "upsert": true, "ops": [{"type": "$set", "path": "", "value": %s}]}]
	}`, objectId, dataview.Dataset, id, payload)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create view %q: %d %s", id, rec.Code, rec.Body.String())
	}
	return decodeModifyResult(t, rec.Body.Bytes())
}

const defaultViewPayload = `{
	"name": "All",
	"icon": "📋",
	"pos": "a0",
	"layout": "table",
	"query": {
		"type": "plain",
		"filter": {"any.types": "page"},
		"sort": ["-modifiedAt"],
		"groupBy": {"propId": "status"}
	},
	"layoutSettings": {
		"visible": ["name", "status"],
		"order": ["name", "status"],
		"widths": {"name": 320, "status": 160}
	}
}`

// TestServer_DataView_CreateReadStamp covers the shared tier end to
// end: a client-supplied id, the server-stamped creator/createdAt/
// modifiedAt, and verbatim round-trip of the opaque query and layout
// blobs.
func TestServer_DataView_CreateReadStamp(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)

	res := createView(t, e, spaceId, objectId, "default", defaultViewPayload)
	if len(res.Rejections) > 0 {
		t.Fatalf("create rejected: %+v", res.Rejections)
	}
	if res.ChangeId == "" {
		t.Errorf("synced write minted no changeId: %+v", res)
	}

	v := getView(t, e, spaceId, objectId, "default")
	if v.Name != "All" || v.Layout != "table" || v.Icon != "📋" || v.Pos != "a0" {
		t.Errorf("scalars round-tripped wrong: %+v", v)
	}
	if v.Creator == "" {
		t.Errorf("creator not stamped")
	}
	if v.CreatedAt == 0 || v.ModifiedAt == 0 {
		t.Errorf("timestamps not stamped: createdAt=%v modifiedAt=%v", v.CreatedAt, v.ModifiedAt)
	}

	// The opaque blobs come back byte-for-byte in structure — nothing
	// in the server inspects or rewrites them.
	var gotQuery, wantQuery map[string]any
	if err := json.Unmarshal(v.Query, &gotQuery); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if err := json.Unmarshal([]byte(`{
		"type": "plain",
		"filter": {"any.types": "page"},
		"sort": ["-modifiedAt"],
		"groupBy": {"propId": "status"}
	}`), &wantQuery); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gotQuery) != fmt.Sprint(wantQuery) {
		t.Errorf("query = %v, want %v", gotQuery, wantQuery)
	}

	// Upserting the same id again is idempotent — still one view.
	createView(t, e, spaceId, objectId, "default", defaultViewPayload)
	if got := len(listViews(t, e, spaceId, objectId)); got != 1 {
		t.Errorf("view count after re-upsert = %d, want 1", got)
	}
}

// TestServer_DataView_EditBumpsModifiedAt pins the declarative
// mutability rules: any writer retunes a shared view, and an accepted
// write bumps modifiedAt while leaving createdAt alone.
func TestServer_DataView_EditBumpsModifiedAt(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)
	createView(t, e, spaceId, objectId, "default", defaultViewPayload)
	before := getView(t, e, spaceId, objectId, "default")

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", fmt.Sprintf(`{
		"objectId": %q, "dataset": %q,
		"records": [{"id": "default", "ops": [
			{"type": "$set", "path": "name", "value": "Urgent"},
			{"type": "$set", "path": "query", "value": {"type": "plain", "filter": {"status": "urgent"}}}
		]}]
	}`, objectId, dataview.Dataset))
	if rec.Code != http.StatusOK {
		t.Fatalf("edit view: %d %s", rec.Code, rec.Body.String())
	}
	if res := decodeModifyResult(t, rec.Body.Bytes()); len(res.Rejections) > 0 {
		t.Fatalf("edit rejected: %+v", res.Rejections)
	}

	after := getView(t, e, spaceId, objectId, "default")
	if after.Name != "Urgent" {
		t.Errorf("name = %q, want Urgent", after.Name)
	}
	if after.CreatedAt != before.CreatedAt {
		t.Errorf("createdAt moved: %v -> %v", before.CreatedAt, after.CreatedAt)
	}
	if after.ModifiedAt < before.ModifiedAt {
		t.Errorf("modifiedAt went backwards: %v -> %v", before.ModifiedAt, after.ModifiedAt)
	}
}

// TestServer_DataView_Required rejects a create missing a required
// field. The generic schema handler screens creation, so the whole
// record is rejected rather than partially applied.
func TestServer_DataView_Required(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)

	for _, tc := range []struct{ name, payload string }{
		{"no name", `{"layout": "table", "pos": "a0"}`},
		{"no layout", `{"name": "All", "pos": "a0"}`},
		{"no pos", `{"name": "All", "layout": "table"}`},
	} {
		body := fmt.Sprintf(`{
			"objectId": %q, "dataset": %q,
			"records": [{"id": "bad", "upsert": true, "ops": [{"type": "$set", "path": "", "value": %s}]}]
		}`, objectId, dataview.Dataset, tc.payload)
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", tc.name, rec.Code, rec.Body.String())
			continue
		}
		if got := errEnvCode(t, rec.Body.Bytes()); got != "dataset.validation" {
			t.Errorf("%s: code = %q, want dataset.validation", tc.name, got)
		}
	}
	if got := len(listViews(t, e, spaceId, objectId)); got != 0 {
		t.Errorf("view count = %d, want 0 — rejected creates must leave nothing", got)
	}
}

// TestServer_DataView_DerivedFieldsRejected: creator / createdAt /
// modifiedAt are handler-owned, so a client op targeting them is
// dropped by the scope enforcement.
func TestServer_DataView_DerivedFieldsRejected(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)
	createView(t, e, spaceId, objectId, "default", defaultViewPayload)
	before := getView(t, e, spaceId, objectId, "default")

	for _, field := range []string{dataview.FieldCreator, dataview.FieldCreatedAt} {
		value := `"impostor"`
		if field == dataview.FieldCreatedAt {
			value = "1"
		}
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", fmt.Sprintf(`{
			"objectId": %q, "dataset": %q,
			"records": [{"id": "default", "ops": [{"type": "$set", "path": %q, "value": %s}]}]
		}`, objectId, dataview.Dataset, field, value))
		if rec.Code == http.StatusOK {
			if res := decodeModifyResult(t, rec.Body.Bytes()); len(res.Rejections) == 0 {
				t.Errorf("write to derived %s accepted, want rejection", field)
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("write %s: %d %s, want 400 or a rejection", field, rec.Code, rec.Body.String())
		}
	}

	after := getView(t, e, spaceId, objectId, "default")
	if after.Creator != before.Creator || after.CreatedAt != before.CreatedAt {
		t.Errorf("derived fields changed: %+v -> %+v", before, after)
	}
}

// TestServer_DataView_LocalSettings covers the device tier: the local
// blob is writable only through the local scope route, never syncs
// (no DAG changeId), and a synced write to it is refused.
func TestServer_DataView_LocalSettings(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)
	createView(t, e, spaceId, objectId, "default", defaultViewPayload)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", fmt.Sprintf(`{
		"objectId": %q, "dataset": %q, "scope": "local",
		"records": [{"id": "default", "ops": [
			{"type": "$set", "path": "localSettings", "value": {"widths": {"name": 480}}}
		]}]
	}`, objectId, dataview.Dataset))
	if rec.Code != http.StatusOK {
		t.Fatalf("local write: %d %s", rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if len(res.Rejections) > 0 {
		t.Fatalf("local write rejected: %+v", res.Rejections)
	}
	if res.ChangeId != "" {
		t.Errorf("local write minted a DAG changeId %q", res.ChangeId)
	}

	v := getView(t, e, spaceId, objectId, "default")
	var local map[string]any
	if err := json.Unmarshal(v.LocalSettings, &local); err != nil {
		t.Fatalf("decode localSettings: %v (raw=%s)", err, v.LocalSettings)
	}
	widths, _ := local["widths"].(map[string]any)
	if widths["name"] != float64(480) {
		t.Errorf("localSettings.widths.name = %v, want 480", widths["name"])
	}

	// The synced route must not reach a local-scope field.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", fmt.Sprintf(`{
		"objectId": %q, "dataset": %q, "scope": "synced",
		"records": [{"id": "default", "ops": [
			{"type": "$set", "path": "localSettings", "value": {"widths": {"name": 999}}}
		]}]
	}`, objectId, dataview.Dataset))
	if rec.Code == http.StatusOK {
		if res := decodeModifyResult(t, rec.Body.Bytes()); len(res.Rejections) == 0 {
			t.Errorf("synced write to a local field accepted, want rejection")
		}
	} else if rec.Code != http.StatusBadRequest {
		t.Errorf("synced write to a local field: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_DataView_DatasetDiscovery pins the behavioral declaration
// on the discovery document — clients read the id rule and the stamps
// from here rather than hardcoding them.
func TestServer_DataView_DatasetDiscovery(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, _ := setupViewFixture(t, e)

	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/datasets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list datasets: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Datasets []struct {
			Name   string `json:"name"`
			Schema struct {
				Id         string `json:"x-id"`
				Properties map[string]struct {
					Scope     string `json:"x-scope"`
					MutableBy string `json:"x-mutable-by"`
					Stamp     string `json:"x-stamp"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"schema"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode datasets: %v", err)
	}
	for _, ds := range resp.Datasets {
		if ds.Name != dataview.Dataset {
			continue
		}
		if ds.Schema.Id != "user" {
			t.Errorf("x-id = %q, want user", ds.Schema.Id)
		}
		if got := ds.Schema.Properties[dataview.FieldLocalSettings].Scope; got != "local" {
			t.Errorf("localSettings x-scope = %q, want local", got)
		}
		if got := ds.Schema.Properties[dataview.FieldName].MutableBy; got != "any" {
			t.Errorf("name x-mutable-by = %q, want any", got)
		}
		if got := ds.Schema.Properties[dataview.FieldModifiedAt].Stamp; got != "modifyTime" {
			t.Errorf("modifiedAt x-stamp = %q, want modifyTime", got)
		}
		if got := fmt.Sprint(ds.Schema.Required); got != "[name pos layout]" {
			t.Errorf("required = %v, want name+pos+layout", ds.Schema.Required)
		}
		return
	}
	t.Fatalf("dataset %q missing from discovery", dataview.Dataset)
}

// TestServer_DataView_XKeyFenced: the registered id occupies the xKey
// namespace, so a client cannot mint a competing user "data_view" type.
func TestServer_DataView_XKeyFenced(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, _ := setupViewFixture(t, e)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		fmt.Sprintf(`{"name":"Data View","xKey":%q}`, dataview.TypeId))
	if rec.Code != http.StatusConflict {
		t.Fatalf("create colliding type: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "type.xkey_conflict" {
		t.Errorf("code = %q, want type.xkey_conflict", got)
	}
}
