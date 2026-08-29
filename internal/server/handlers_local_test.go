package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// localDo sends a JSON request to a /v1/local route and decodes the
// body on the expected status.
func localDo[T any](t *testing.T, e http.Handler, method, path, body string, wantStatus int) *T {
	t.Helper()
	rec := doJSON(t, e, method, path, body)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: status=%d, want %d; body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	if wantStatus == http.StatusNoContent {
		return nil
	}
	out := new(T)
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %s %s: %v: %s", method, path, err, rec.Body.String())
	}
	return out
}

func localExpectError(t *testing.T, e http.Handler, method, path, body string, wantStatus int, wantCode string) api.APIError {
	t.Helper()
	rec := doJSON(t, e, method, path, body)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: status=%d, want %d; body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v: %s", err, rec.Body.String())
	}
	if env.Error.Code != wantCode {
		t.Errorf("%s %s: code=%q, want %q (message: %s)", method, path, env.Error.Code, wantCode, env.Error.Message)
	}
	return env.Error
}

const accColl = `{"scope":"account","name":"notes"}`

func localEnsure(t *testing.T, e http.Handler, coll string, wantStatus int) *api.LocalEnsureResponse {
	t.Helper()
	return localDo[api.LocalEnsureResponse](t, e, http.MethodPut, "/v1/local/collections", coll, wantStatus)
}

func TestServer_Local_Disabled(t *testing.T) {
	d, teardown := newTestDepsCfg(t, func(c *config.Config) { c.Local.Enabled = false })
	defer teardown()
	e := buildEcho(d)

	localExpectError(t, e, http.MethodGet, "/v1/local/meta", "", http.StatusConflict, "local.disabled")
	localExpectError(t, e, http.MethodGet, "/v1/local/collections", "", http.StatusConflict, "local.disabled")
	localExpectError(t, e, http.MethodPut, "/v1/local/collections", accColl, http.StatusConflict, "local.disabled")
	localExpectError(t, e, http.MethodPost, "/v1/local/query", `{"coll":`+accColl+`}`, http.StatusConflict, "local.disabled")
}

func TestServer_Local_Meta(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	meta := localDo[api.LocalMetaResponse](t, e, http.MethodGet, "/v1/local/meta", "", http.StatusOK)
	for _, want := range []string{"$match", "$group", "$lookup", "$out", "$merge", "$facet"} {
		found := false
		for _, s := range meta.Stages {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("stages missing %s: %v", want, meta.Stages)
		}
	}
	if len(meta.Accumulators) == 0 {
		t.Errorf("no accumulators advertised")
	}
}

func TestServer_Local_Collections(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// Nothing yet.
	list := localDo[api.LocalListResponse](t, e, http.MethodGet, "/v1/local/collections", "", http.StatusOK)
	if len(list.Collections) != 0 {
		t.Fatalf("fresh store lists %d collections", len(list.Collections))
	}
	// Any op on an un-ensured collection is 404.
	localExpectError(t, e, http.MethodPost, "/v1/local/query", `{"coll":`+accColl+`}`, http.StatusNotFound, "local.collection_not_found")

	// Ensure: 201 then 200; indexes ride along.
	created := localEnsure(t, e, `{"scope":"account","name":"notes","indexes":[{"fields":["kind","-at"]}]}`, http.StatusCreated)
	if !created.Created || created.Collection.StorageName != "l_a_notes" || created.Collection.Count != 0 {
		t.Fatalf("created: %+v", created)
	}
	if len(created.Collection.Indexes) != 1 || created.Collection.Indexes[0].Name != "kind,-at" {
		t.Fatalf("indexes after ensure: %+v", created.Collection.Indexes)
	}
	again := localEnsure(t, e, accColl, http.StatusOK)
	if again.Created {
		t.Fatalf("second ensure reported created")
	}

	// Bad names / scopes.
	localExpectError(t, e, http.MethodPut, "/v1/local/collections", `{"scope":"account","name":"Bad Name"}`, http.StatusBadRequest, "local.bad_name")
	localExpectError(t, e, http.MethodPut, "/v1/local/collections", `{"scope":"device","name":"x"}`, http.StatusBadRequest, "local.bad_name")
	localExpectError(t, e, http.MethodPut, "/v1/local/collections", `{"scope":"space","name":"x"}`, http.StatusBadRequest, "local.bad_name")
	localExpectError(t, e, http.MethodPut, "/v1/local/collections", `{"scope":"account","name":"x","extra":1}`, http.StatusBadRequest, "request.unknown_field")
	// Space scope on a space this account doesn't have.
	localExpectError(t, e, http.MethodPut, "/v1/local/collections",
		`{"scope":"space","spaceId":"bafyreiaybsrmdicsv7fmuupnzojtdnhpna7its6evawg7lhw67pu4yiulq.1f5isugmolo56","name":"x"}`,
		http.StatusNotFound, "space.not_found")

	// A real space: ensure, list by space, drop.
	spaceId, _, _ := setupSubscribeFixture(t, e)
	spaceColl := fmt.Sprintf(`{"scope":"space","spaceId":%q,"name":"cache"}`, spaceId)
	sc := localEnsure(t, e, spaceColl, http.StatusCreated)
	if sc.Collection.StorageName != "l_s_"+spaceId+"_cache" {
		t.Fatalf("space storage name: %s", sc.Collection.StorageName)
	}
	list = localDo[api.LocalListResponse](t, e, http.MethodGet, "/v1/local/collections", "", http.StatusOK)
	if len(list.Collections) != 2 {
		t.Fatalf("list all: %+v", list.Collections)
	}
	list = localDo[api.LocalListResponse](t, e, http.MethodGet, "/v1/local/collections?spaceId="+spaceId, "", http.StatusOK)
	if len(list.Collections) != 1 || list.Collections[0].Name != "cache" || list.Collections[0].SpaceId != spaceId {
		t.Fatalf("list by space: %+v", list.Collections)
	}
	list = localDo[api.LocalListResponse](t, e, http.MethodGet, "/v1/local/collections?scope=account", "", http.StatusOK)
	if len(list.Collections) != 1 || list.Collections[0].Name != "notes" {
		t.Fatalf("list account: %+v", list.Collections)
	}
	localExpectError(t, e, http.MethodGet, "/v1/local/collections?scope=device", "", http.StatusBadRequest, "local.bad_name")
	localExpectError(t, e, http.MethodGet, "/v1/local/collections?scope=account&spaceId="+spaceId, "", http.StatusBadRequest, "local.bad_name")

	// A rejected index leaves no collection behind (create + index are
	// one transaction) and is a client error.
	localExpectError(t, e, http.MethodPut, "/v1/local/collections",
		`{"scope":"account","name":"badidx","indexes":[{"name":"a","fields":["x"]},{"name":"a","fields":["y"]}]}`, http.StatusBadRequest, "local.bad_index")
	localExpectError(t, e, http.MethodPost, "/v1/local/query", `{"coll":{"scope":"account","name":"badidx"}}`, http.StatusNotFound, "local.collection_not_found")

	localDo[struct{}](t, e, http.MethodDelete, "/v1/local/collections?scope=space&spaceId="+spaceId+"&name=cache", "", http.StatusNoContent)
	localExpectError(t, e, http.MethodDelete, "/v1/local/collections?scope=space&spaceId="+spaceId+"&name=cache", "", http.StatusNotFound, "local.collection_not_found")
	list = localDo[api.LocalListResponse](t, e, http.MethodGet, "/v1/local/collections", "", http.StatusOK)
	if len(list.Collections) != 1 {
		t.Fatalf("after drop: %+v", list.Collections)
	}
}

func TestServer_Local_Documents(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	localEnsure(t, e, accColl, http.StatusCreated)

	// Insert: explicit + minted ids, request order preserved.
	ins := localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/insert",
		`{"coll":`+accColl+`,"docs":[{"id":"a","kind":"x","n":1},{"kind":"y","n":2},{"id":"c","kind":"x","n":3}]}`, http.StatusOK)
	if len(ins.Ids) != 3 || ins.Ids[0] != "a" || ins.Ids[2] != "c" || ins.Ids[1] == "" || ins.Ids[1] == "a" {
		t.Fatalf("insert ids: %v", ins.Ids)
	}
	minted := ins.Ids[1]

	// Duplicate → 409. Bad shapes → 400.
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`,"docs":[{"id":"a"}]}`, http.StatusConflict, "local.duplicate_id")
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`,"docs":[]}`, http.StatusBadRequest, "request.missing_field")
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`,"docs":[1]}`, http.StatusBadRequest, "request.schema")
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`,"docs":[{"id":7}]}`, http.StatusBadRequest, "request.schema")
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`}`, http.StatusBadRequest, "request.missing_field")
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"docs":[{}]}`, http.StatusBadRequest, "request.missing_field")
	var many strings.Builder
	many.WriteString(`{"coll":` + accColl + `,"docs":[`)
	for i := 0; i <= localMaxDocs; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `{"id":"m%d"}`, i)
	}
	many.WriteString("]}")
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", many.String(), http.StatusBadRequest, "local.too_many_docs")

	// Get.
	got := localDo[api.LocalRecordResponse](t, e, http.MethodPost, "/v1/local/get", `{"coll":`+accColl+`,"id":"a"}`, http.StatusOK)
	if !strings.Contains(string(got.Record), `"kind":"x"`) {
		t.Fatalf("get a: %s", got.Record)
	}
	localExpectError(t, e, http.MethodPost, "/v1/local/get", `{"coll":`+accColl+`,"id":"zz"}`, http.StatusNotFound, "local.doc_not_found")

	// Update with a modifier; missing id 404 unless upsert.
	up := localDo[api.LocalUpdateResponse](t, e, http.MethodPost, "/v1/local/update",
		`{"coll":`+accColl+`,"id":"a","modifier":{"$set":{"kind":"z"},"$inc":{"n":10}}}`, http.StatusOK)
	if !up.Modified || !strings.Contains(string(up.Record), `"kind":"z"`) || !strings.Contains(string(up.Record), `"n":11`) {
		t.Fatalf("update: %+v %s", up.Modified, up.Record)
	}
	localExpectError(t, e, http.MethodPost, "/v1/local/update", `{"coll":`+accColl+`,"id":"zz","modifier":{"$set":{"k":1}}}`, http.StatusNotFound, "local.doc_not_found")
	up = localDo[api.LocalUpdateResponse](t, e, http.MethodPost, "/v1/local/update",
		`{"coll":`+accColl+`,"id":"zz","modifier":{"$set":{"kind":"new"}},"upsert":true}`, http.StatusOK)
	if !strings.Contains(string(up.Record), `"id":"zz"`) {
		t.Fatalf("upsert-update: %s", up.Record)
	}
	localExpectError(t, e, http.MethodPost, "/v1/local/update", `{"coll":`+accColl+`,"id":"a","modifier":{"$bogus":{"k":1}}}`, http.StatusBadRequest, "local.bad_modifier")

	// Upsert whole docs: replace a, add d.
	ups := localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/upsert",
		`{"coll":`+accColl+`,"docs":[{"id":"a","kind":"x","n":1},{"id":"d","kind":"y","n":4}]}`, http.StatusOK)
	if len(ups.Ids) != 2 {
		t.Fatalf("upsert ids: %v", ups.Ids)
	}
	got = localDo[api.LocalRecordResponse](t, e, http.MethodPost, "/v1/local/get", `{"coll":`+accColl+`,"id":"a"}`, http.StatusOK)
	if !strings.Contains(string(got.Record), `"kind":"x"`) || strings.Contains(string(got.Record), `"n":11`) {
		t.Fatalf("upsert replaced a: %s", got.Record)
	}

	// Query: filter, sort, paging, total.
	q := localDo[api.QueryResponse](t, e, http.MethodPost, "/v1/local/query",
		`{"coll":`+accColl+`,"filter":{"kind":"x"},"sort":["-n"],"includeTotal":true}`, http.StatusOK)
	if len(q.Records) != 2 || q.Total == nil || *q.Total != 2 || q.HasNext == nil || *q.HasNext {
		t.Fatalf("query x: %d records total=%v hasNext=%v", len(q.Records), q.Total, q.HasNext)
	}
	if !strings.Contains(string(q.Records[0]), `"id":"c"`) {
		t.Fatalf("sort -n: first=%s", q.Records[0])
	}
	q = localDo[api.QueryResponse](t, e, http.MethodPost, "/v1/local/query",
		`{"coll":`+accColl+`,"sort":["n"],"limit":2,"offset":1,"includeTotal":true}`, http.StatusOK)
	if len(q.Records) != 2 || *q.Total != 5 || !*q.HasNext {
		t.Fatalf("paged: %d total=%d hasNext=%v", len(q.Records), *q.Total, *q.HasNext)
	}
	localExpectError(t, e, http.MethodPost, "/v1/local/query", `{"coll":`+accColl+`,"filter":{"n":{"$bogus":1}}}`, http.StatusBadRequest, "filter.unknown_operator")
	localExpectError(t, e, http.MethodPost, "/v1/local/query", `{"coll":`+accColl+`,"limit":"x","filters":{}}`, http.StatusBadRequest, "request.unknown_field")

	// Delete by ids (missing ids are not counted, not errors).
	del := localDo[api.LocalDeleteResponse](t, e, http.MethodPost, "/v1/local/delete",
		`{"coll":`+accColl+`,"ids":["d","nope"]}`, http.StatusOK)
	if del.Deleted != 1 {
		t.Fatalf("delete by ids: %d", del.Deleted)
	}
	localExpectError(t, e, http.MethodPost, "/v1/local/delete", `{"coll":`+accColl+`}`, http.StatusBadRequest, "request.missing_field")
	localExpectError(t, e, http.MethodPost, "/v1/local/delete", `{"coll":`+accColl+`,"ids":["a"],"filter":{}}`, http.StatusBadRequest, "request.missing_field")

	// Delete by filter across more than one write chunk.
	var bulk strings.Builder
	bulk.WriteString(`{"coll":` + accColl + `,"docs":[`)
	n := localWriteChunk*2 + 7
	for i := 0; i < n; i++ {
		if i > 0 {
			bulk.WriteString(",")
		}
		fmt.Fprintf(&bulk, `{"id":"b%d","bulk":true}`, i)
	}
	bulk.WriteString("]}")
	localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/insert", bulk.String(), http.StatusOK)
	del = localDo[api.LocalDeleteResponse](t, e, http.MethodPost, "/v1/local/delete",
		`{"coll":`+accColl+`,"filter":{"bulk":true}}`, http.StatusOK)
	if del.Deleted != n {
		t.Fatalf("delete by filter: %d, want %d", del.Deleted, n)
	}
	q = localDo[api.QueryResponse](t, e, http.MethodPost, "/v1/local/query", `{"coll":`+accColl+`,"includeTotal":true}`, http.StatusOK)
	if *q.Total != 4 {
		t.Fatalf("after deletes total=%d (%v)", *q.Total, minted)
	}
}

func TestServer_Local_Indexes(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	localEnsure(t, e, accColl, http.StatusCreated)

	ix := localDo[api.LocalIndexesResponse](t, e, http.MethodPost, "/v1/local/indexes",
		`{"coll":`+accColl+`,"ensure":[{"fields":["key"],"unique":true},{"name":"by_kind","fields":["kind"]}]}`, http.StatusOK)
	if len(ix.Indexes) != 2 {
		t.Fatalf("indexes: %+v", ix.Indexes)
	}
	localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`,"docs":[{"id":"1","key":"k"}]}`, http.StatusOK)
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`,"docs":[{"id":"2","key":"k"}]}`, http.StatusConflict, "local.unique_violation")

	ix = localDo[api.LocalIndexesResponse](t, e, http.MethodPost, "/v1/local/indexes",
		`{"coll":`+accColl+`,"drop":["key","by_kind","never_existed"]}`, http.StatusOK)
	if len(ix.Indexes) != 0 {
		t.Fatalf("after drop: %+v", ix.Indexes)
	}
	localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+accColl+`,"docs":[{"id":"2","key":"k"}]}`, http.StatusOK)
	localExpectError(t, e, http.MethodPost, "/v1/local/indexes", `{"coll":`+accColl+`,"ensure":[{"unique":true}]}`, http.StatusBadRequest, "request.schema")
}

func TestServer_Local_Aggregate(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	localEnsure(t, e, accColl, http.StatusCreated)
	localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/insert",
		`{"coll":`+accColl+`,"docs":[{"id":"1","kind":"x","n":1},{"id":"2","kind":"x","n":2},{"id":"3","kind":"y","n":5}]}`, http.StatusOK)

	// $group: key comes back as id.
	agg := localDo[api.LocalAggregateResponse](t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$group":{"_id":"$kind","sum":{"$sum":"$n"}}},{"$sort":{"id":1}}]}`, http.StatusOK)
	if len(agg.Records) != 2 || !strings.Contains(string(agg.Records[0]), `"id":"x"`) || !strings.Contains(string(agg.Records[0]), `"sum":3`) {
		t.Fatalf("group: %s", agg.Records)
	}
	// Explain.
	agg = localDo[api.LocalAggregateResponse](t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$match":{"kind":"x"}}],"explain":true}`, http.StatusOK)
	if agg.Plan == nil || agg.Records != nil {
		t.Fatalf("explain: %+v", agg)
	}
	// Bad pipeline / limits.
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate", `{"coll":`+accColl+`,"pipeline":[{"$bogus":{}}]}`, http.StatusBadRequest, "local.bad_pipeline")
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate", `{"coll":`+accColl+`,"pipeline":{}}`, http.StatusBadRequest, "request.schema")
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate", `{"coll":`+accColl+`}`, http.StatusBadRequest, "request.missing_field")
	err := localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$group":{"_id":"$id"}}],"groupLimit":1}`, http.StatusBadRequest, "local.limit_exceeded")
	if err.Details["limit"] != "group" {
		t.Errorf("limit detail: %v", err.Details)
	}

	// $out into a local collection: the target must exist (a sink never
	// mints a collection), then written count, then readable.
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$group":{"_id":"$kind"}},{"$out":"l_a_rollup"}]}`, http.StatusNotFound, "local.collection_not_found")
	localEnsure(t, e, `{"scope":"account","name":"rollup"}`, http.StatusCreated)
	agg = localDo[api.LocalAggregateResponse](t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$group":{"_id":"$kind","sum":{"$sum":"$n"}}},{"$out":"l_a_rollup"}]}`, http.StatusOK)
	if agg.Written == nil || *agg.Written != 2 || agg.Records != nil {
		t.Fatalf("$out: %+v", agg)
	}
	q := localDo[api.QueryResponse](t, e, http.MethodPost, "/v1/local/query", `{"coll":{"scope":"account","name":"rollup"},"sort":["id"]}`, http.StatusOK)
	if len(q.Records) != 2 || !strings.Contains(string(q.Records[1]), `"id":"y"`) {
		t.Fatalf("rollup: %s", q.Records)
	}
	// $merge into the same target: idempotent re-run.
	agg = localDo[api.LocalAggregateResponse](t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$group":{"_id":"$kind","sum":{"$sum":"$n"}}},{"$merge":{"into":"l_a_rollup"}}]}`, http.StatusOK)
	if agg.Written == nil || *agg.Written != 2 {
		t.Fatalf("$merge: %+v", agg)
	}
	// $out into the source is refused by any-store.
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$out":"l_a_notes"}]}`, http.StatusBadRequest, "local.bad_pipeline")

	// The fence: untagged sink / lookup targets never reach the store.
	for _, stage := range []string{
		`{"$out":"_meta"}`,
		`{"$out":"bafyreiaybsrmdicsv7fmuupnzojtdnhpna7its6evawg7lhw67pu4yiulq.1f5isugmolo56_objects"}`,
		`{"$merge":{"into":"rollup"}}`,
		`{"$merge":"cursors"}`,
		`{"$lookup":{"from":"_meta","localField":"id","foreignField":"id","as":"m"}}`,
	} {
		localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
			`{"coll":`+accColl+`,"pipeline":[`+stage+`]}`, http.StatusBadRequest, "local.bad_sink_target")
	}
	// A tagged but unparseable target is a bad name, also fenced.
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$out":"l_x_nope"}]}`, http.StatusBadRequest, "local.bad_sink_target")
	// A sink nested in $facet is fenced too; a $lookup from another
	// local collection is a client error, not a 500; $project dropping
	// id before $out is a client error.
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$facet":{"a":[{"$out":"_meta"}]}}]}`, http.StatusBadRequest, "local.bad_sink_target")
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$lookup":{"from":"l_a_rollup","localField":"kind","foreignField":"id","as":"r"}}]}`, http.StatusBadRequest, "local.bad_pipeline")
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$project":{"id":0,"n":1}},{"$out":"l_a_rollup"}]}`, http.StatusBadRequest, "local.bad_pipeline")
	// A sink into a space-scoped target runs the space pre-flight.
	localExpectError(t, e, http.MethodPost, "/v1/local/aggregate",
		`{"coll":`+accColl+`,"pipeline":[{"$out":"l_s_bafyreiaybsrmdicsv7fmuupnzojtdnhpna7its6evawg7lhw67pu4yiulq.1f5isugmolo56_x"}]}`, http.StatusNotFound, "space.not_found")
	// The meta collection was never created by any of the above.
	list := localDo[api.LocalListResponse](t, e, http.MethodGet, "/v1/local/collections", "", http.StatusOK)
	if len(list.Collections) != 2 {
		t.Fatalf("collections after fence tests: %+v", list.Collections)
	}
}

// TestServer_Local_ConcurrentBodies pins that a request's parsed body
// is never shared with another in-flight request: N collections each
// receive exactly their own docs under concurrent inserts, updates
// and deletes (run with -race).
func TestServer_Local_ConcurrentBodies(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	const workers, rounds = 8, 20
	for w := 0; w < workers; w++ {
		localEnsure(t, e, fmt.Sprintf(`{"scope":"account","name":"c%d"}`, w), http.StatusCreated)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			coll := fmt.Sprintf(`{"scope":"account","name":"c%d"}`, w)
			for r := 0; r < rounds; r++ {
				rec := doJSON(t, e, http.MethodPost, "/v1/local/insert",
					fmt.Sprintf(`{"coll":%s,"docs":[{"id":"r%d","w":%d},{"w":%d}]}`, coll, r, w, w))
				if rec.Code != http.StatusOK {
					t.Errorf("worker %d insert: %d %s", w, rec.Code, rec.Body.String())
					return
				}
				rec = doJSON(t, e, http.MethodPost, "/v1/local/update",
					fmt.Sprintf(`{"coll":%s,"id":"r%d","modifier":{"$set":{"seen":true}}}`, coll, r))
				if rec.Code != http.StatusOK {
					t.Errorf("worker %d update: %d %s", w, rec.Code, rec.Body.String())
					return
				}
				rec = doJSON(t, e, http.MethodPost, "/v1/local/delete",
					fmt.Sprintf(`{"coll":%s,"filter":{"w":{"$ne":%d}}}`, coll, w))
				if rec.Code != http.StatusOK {
					t.Errorf("worker %d delete: %d %s", w, rec.Code, rec.Body.String())
					return
				}
			}
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		q := localDo[api.QueryResponse](t, e, http.MethodPost, "/v1/local/query",
			fmt.Sprintf(`{"coll":{"scope":"account","name":"c%d"},"includeTotal":true,"limit":1000}`, w), http.StatusOK)
		if *q.Total != rounds*2 {
			t.Errorf("collection c%d: %d docs, want %d", w, *q.Total, rounds*2)
		}
		for _, r := range q.Records {
			if !strings.Contains(string(r), fmt.Sprintf(`"w":%d`, w)) {
				t.Errorf("collection c%d holds a foreign doc: %s", w, r)
			}
		}
	}
}
