package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/localstore"
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

// Export → drop → import round-trips a space-scoped collection with
// its indexes and documents through the wire; the file is gzip with
// the manifest as its first anyenc value; the failure modes are
// typed. The imported space is never pre-flighted — the collection
// comes back on a server that has never seen the space.
func TestServer_Local_ExportImport(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	const ghostSpace = "bafyreiaybsrmdicsv7fmuupnzojtdnhpna7its6evawg7lhw67pu4yiulq.1f5isugmolo56"
	// a space-scoped collection needs its space to exist for ensure /
	// insert, so seed it under a real space, then export by name
	sp := createSpaceInfo(t, e, "export").Id
	coll := fmt.Sprintf(`{"scope":"space","spaceId":%q,"name":"trace_records"}`, sp)
	localEnsure(t, e, `{"scope":"space","spaceId":"`+sp+`","name":"trace_records","indexes":[{"fields":["runId","seq"],"unique":true}]}`, http.StatusCreated)
	localDo[api.LocalIdsResponse](t, e, http.MethodPost, "/v1/local/insert",
		`{"coll":`+coll+`,"docs":[{"id":"run_1:0","runId":"run_1","seq":0,"kind":"header"},{"id":"run_1:1","runId":"run_1","seq":1,"input":{"q":"héllo"}}]}`, http.StatusOK)
	localEnsure(t, e, accColl, http.StatusCreated)

	// unknown name → 404 before any byte; bad scope → 400
	localExpectError(t, e, http.MethodGet, "/v1/local/export?spaceId="+sp+"&names=nope", "", http.StatusNotFound, "local.collection_not_found")
	localExpectError(t, e, http.MethodGet, "/v1/local/export?names=trace_records", "", http.StatusBadRequest, "local.bad_name")
	localExpectError(t, e, http.MethodGet, "/v1/local/export?spaceId="+ghostSpace, "", http.StatusNotFound, "local.collection_not_found")

	rec := doJSON(t, e, http.MethodGet, "/v1/local/export?spaceId="+sp+"&names=trace_records", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("export content-type = %q", ct)
	}
	file := rec.Body.Bytes()
	gz, err := gzip.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatalf("export is not gzip: %v", err)
	}
	mv, err := anyenc.NewReader(gz).Read(&anyenc.Parser{})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if got := mv.GetString("format"); got != localstore.FormatName {
		t.Fatalf("manifest format = %q", got)
	}
	if n := mv.GetInt("collections", "0", "count"); n != 2 {
		t.Fatalf("manifest count = %d, want 2; manifest %s", n, mv.String())
	}

	// the whole scope, no names: both the space's collections would be
	// here if there were two — one here, plus the account one stays out
	rec = doJSON(t, e, http.MethodGet, "/v1/local/export?spaceId="+sp, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export scope: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// drop, then import the file: same name, index and documents
	localDo[struct{}](t, e, http.MethodDelete, "/v1/local/collections?scope=space&spaceId="+sp+"&name=trace_records", "", http.StatusNoContent)
	imp := doRaw(t, e, http.MethodPost, "/v1/local/import", "application/gzip", file)
	if imp.Code != http.StatusOK {
		t.Fatalf("import: status=%d body=%s", imp.Code, imp.Body.String())
	}
	var out api.LocalImportResponse
	if err := json.Unmarshal(imp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Collections) != 1 || out.Collections[0].Count != 2 || out.Collections[0].SpaceId != sp || len(out.Collections[0].Indexes) != 1 {
		t.Fatalf("import reply: %s", imp.Body.String())
	}
	got := localDo[api.LocalRecordResponse](t, e, http.MethodPost, "/v1/local/get", `{"coll":`+coll+`,"id":"run_1:1"}`, http.StatusOK)
	if !strings.Contains(string(got.Record), `"héllo"`) {
		t.Fatalf("imported record: %s", got.Record)
	}
	// unique index came back: a clashing insert is refused
	localExpectError(t, e, http.MethodPost, "/v1/local/insert", `{"coll":`+coll+`,"docs":[{"id":"x","runId":"run_1","seq":1}]}`, http.StatusConflict, "local.unique_violation")

	// re-import: idempotent
	imp = doRaw(t, e, http.MethodPost, "/v1/local/import", "application/gzip", file)
	if imp.Code != http.StatusOK {
		t.Fatalf("re-import: status=%d body=%s", imp.Code, imp.Body.String())
	}

	// a file whose space this server has never seen imports as-is
	rec = doJSON(t, e, http.MethodGet, "/v1/local/export?spaceId="+sp+"&names=trace_records", "")
	d2, teardown2 := newTestDeps(t)
	defer teardown2()
	e2 := buildEcho(d2)
	imp = doRaw(t, e2, http.MethodPost, "/v1/local/import", "application/gzip", rec.Body.Bytes())
	if imp.Code != http.StatusOK {
		t.Fatalf("import on a stranger: status=%d body=%s", imp.Code, imp.Body.String())
	}
	list := localDo[api.LocalListResponse](t, e2, http.MethodGet, "/v1/local/collections?spaceId="+sp, "", http.StatusOK)
	if len(list.Collections) != 1 || list.Collections[0].Count != 2 {
		t.Fatalf("stranger's listing: %+v", list.Collections)
	}
	// …and readable there: reads, aggregate and delete skip the space
	// pre-flight; writes that could grow the namespace still 404
	q := localDo[api.QueryResponse](t, e2, http.MethodPost, "/v1/local/query", `{"coll":`+coll+`,"filter":{"runId":"run_1"},"sort":["seq"]}`, http.StatusOK)
	if len(q.Records) != 2 {
		t.Fatalf("stranger's query: %d records", len(q.Records))
	}
	localDo[api.LocalRecordResponse](t, e2, http.MethodPost, "/v1/local/get", `{"coll":`+coll+`,"id":"run_1:0"}`, http.StatusOK)
	agg := localDo[api.LocalAggregateResponse](t, e2, http.MethodPost, "/v1/local/aggregate", `{"coll":`+coll+`,"pipeline":[{"$group":{"_id":"$runId","n":{"$count":{}}}}]}`, http.StatusOK)
	if len(agg.Records) != 1 {
		t.Fatalf("stranger's aggregate: %s", imp.Body.String())
	}
	localExpectError(t, e2, http.MethodPost, "/v1/local/insert", `{"coll":`+coll+`,"docs":[{"id":"z"}]}`, http.StatusNotFound, "space.not_found")
	localExpectError(t, e2, http.MethodPost, "/v1/local/indexes", `{"coll":`+coll+`,"ensure":[{"fields":["kind"]}]}`, http.StatusNotFound, "space.not_found")
	del := localDo[api.LocalDeleteResponse](t, e2, http.MethodPost, "/v1/local/delete", `{"coll":`+coll+`,"ids":["run_1:0"]}`, http.StatusOK)
	if del.Deleted != 1 {
		t.Fatalf("stranger's delete: %+v", del)
	}

	// truncated file → 400 local.bad_export naming the collection
	truncated := doRaw(t, e2, http.MethodPost, "/v1/local/import", "application/gzip", file[:len(file)/2])
	if truncated.Code != http.StatusBadRequest {
		t.Fatalf("truncated import: status=%d body=%s", truncated.Code, truncated.Body.String())
	}
	var env api.ErrorEnvelope
	_ = json.Unmarshal(truncated.Body.Bytes(), &env)
	if env.Error.Code != "local.bad_export" {
		t.Fatalf("truncated import code = %q (%s)", env.Error.Code, env.Error.Message)
	}
	notGz := doRaw(t, e2, http.MethodPost, "/v1/local/import", "application/gzip", []byte("nope"))
	if notGz.Code != http.StatusBadRequest {
		t.Fatalf("not-gzip import: status=%d", notGz.Code)
	}

	// disabled server: both routes 409
	d3, teardown3 := newTestDepsCfg(t, func(c *config.Config) { c.Local.Enabled = false })
	defer teardown3()
	e3 := buildEcho(d3)
	localExpectError(t, e3, http.MethodGet, "/v1/local/export?scope=account", "", http.StatusConflict, "local.disabled")
	localExpectError(t, e3, http.MethodPost, "/v1/local/import", "", http.StatusConflict, "local.disabled")
}
