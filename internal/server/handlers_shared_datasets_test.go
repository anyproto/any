package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// sharedLabPart declares three records datasets on one part: samples
// (shared, caller ids, one index, two fields no index takes), events
// (shared, derived ids, no index) and notes (per object, caller ids, a
// sparse descending index).
const sharedLabPart = `{"key":"lab","name":"Lab","datasets":[
	{"key":"samples","shared":true,"idRule":"user","fields":[
		{"key":"label","kind":"string","mutableBy":"any"},
		{"key":"score","kind":"number","mutableBy":"any"},
		{"key":"kind","kind":"string"},
		{"key":"tags","kind":"array","mutableBy":"any","shape":{"kind":"array","items":{"kind":"string"}}},
		{"key":"meta","kind":"object","mutableBy":"any"}],
	 "indexes":[{"key":"by_kind","fields":["kind","-score","_objectId"]}]},
	{"key":"events","shared":true,"fields":[
		{"key":"label","kind":"string","mutableBy":"any"}]},
	{"key":"notes","idRule":"user","fields":[
		{"key":"label","kind":"string","mutableBy":"any"},
		{"key":"score","kind":"number","mutableBy":"any"}],
	 "indexes":[{"key":"top","fields":["-score"],"sparse":true}]}]}`

// sharedLab is a space whose type declares sharedLabPart, with three
// objects of that type.
type sharedLab struct {
	spaceId, typeId, partId string
	samples, events, notes  string            // storage collections
	defs                    map[string]string // dataset key → definition id
	objs                    []string
}

// setupSharedLab creates the space, the type with its part and three
// objects of the type.
func setupSharedLab(t *testing.T, e http.Handler) *sharedLab {
	t.Helper()
	l := &sharedLab{spaceId: mustCreateSpace(t, e, "SharedLab"), defs: map[string]string{}}
	rec := doJSON(t, e, http.MethodPost, l.base()+"/types", `{"name":"Experiment","xKey":"experiment"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var typ api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typ); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	l.typeId = typ.TypeId
	l.partId = mustAddPart(t, e, l.spaceId, l.typeId, sharedLabPart)
	l.samples, l.events, l.notes = l.typeId+"_samples", l.typeId+"_events", l.typeId+"_notes"
	for _, def := range l.datasetDefs(t, e) {
		l.defs[def.Key] = def.Id
	}
	for range 3 {
		l.objs = append(l.objs, mustCreateObject(t, e, l.spaceId, `{"type":"`+l.typeId+`"}`))
	}
	return l
}

func (l *sharedLab) base() string { return "/v1/spaces/" + l.spaceId }

// datasetDefs reads the type's dataset definitions.
func (l *sharedLab) datasetDefs(t *testing.T, e http.Handler) []api.DatasetDefResponse {
	t.Helper()
	var list api.TypeDatasetsListResponse
	decodeGet(t, e, l.base()+"/types/"+l.typeId+"/datasets", &list)
	return list.Datasets
}

// def returns the type's definition of the dataset key.
func (l *sharedLab) def(t *testing.T, e http.Handler, key string) api.DatasetDefResponse {
	t.Helper()
	for _, def := range l.datasetDefs(t, e) {
		if def.Key == key {
			return def
		}
	}
	t.Fatalf("dataset %q not listed", key)
	return api.DatasetDefResponse{}
}

// discovered returns the discovery row of collection.
func (l *sharedLab) discovered(t *testing.T, e http.Handler, collection string) api.DatasetSchema {
	t.Helper()
	var ds api.DatasetsResponse
	decodeGet(t, e, l.base()+"/datasets", &ds)
	for _, s := range ds.Datasets {
		if s.Name == collection {
			return s
		}
	}
	t.Fatalf("%s missing from discovery", collection)
	return api.DatasetSchema{}
}

// putBody is a /modify body creating record id on objectId with one
// multi-field $set.
func putBody(objectId, dataset, id string, fields map[string]any) string {
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId, "dataset": dataset,
		"records": []any{map[string]any{"id": id, "upsert": true,
			"ops": []any{map[string]any{"type": "$set", "value": fields}}}},
	})
	return string(body)
}

// setBody is a /modify body writing one field of record id on objectId.
func setBody(objectId, dataset, id, path string, value any) string {
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId, "dataset": dataset,
		"records": []any{map[string]any{"id": id,
			"ops": []any{map[string]any{"type": "$set", "path": path, "value": value}}}},
	})
	return string(body)
}

func (l *sharedLab) put(t *testing.T, e http.Handler, objectId, dataset, id string, fields map[string]any) api.ModifyResult {
	t.Helper()
	return mustModify(t, e, http.MethodPost, l.base()+"/modify", putBody(objectId, dataset, id, fields), http.StatusOK)
}

func (l *sharedLab) set(t *testing.T, e http.Handler, objectId, dataset, id, path string, value any) api.ModifyResult {
	t.Helper()
	return mustModify(t, e, http.MethodPost, l.base()+"/modify", setBody(objectId, dataset, id, path, value), http.StatusOK)
}

// sharedRow is one samples record of the seed.
type sharedRow struct {
	obj, id, label, kind string
	score                int
}

// seedRows are the samples rows the read tests start from: two on the
// first object, two on the second under the same plain ids, one on the
// third.
func (l *sharedLab) seedRows() []sharedRow {
	a, b, c := l.objs[0], l.objs[1], l.objs[2]
	return []sharedRow{
		{a, "r1", "a1", "x", 10}, {a, "r2", "a2", "y", 20},
		{b, "r1", "b1", "x", 30}, {b, "r2", "b2", "y", 40},
		{c, "r1", "c1", "x", 50},
	}
}

// seed writes seedRows, and a notes row per object that no samples
// read may return.
func (l *sharedLab) seed(t *testing.T, e http.Handler) {
	t.Helper()
	for _, r := range l.seedRows() {
		l.put(t, e, r.obj, l.samples, r.id, map[string]any{"label": r.label, "score": r.score, "kind": r.kind})
	}
	for _, obj := range l.objs {
		l.put(t, e, obj, l.notes, "r1", map[string]any{"label": "note", "score": 1})
	}
}

// sharedId is the id a shared dataset's record reads back under.
func sharedId(objectId, recordId string) string { return objectId + "/" + recordId }

// postQuery POSTs a query body expecting 200 and returns the reply with
// its records decoded.
func postQuery(t *testing.T, e http.Handler, path, body string) (api.QueryResponse, []map[string]any) {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, path, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s %s: %d %s", path, body, rec.Code, rec.Body.String())
	}
	var resp api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode query reply: %v (%s)", err, rec.Body.String())
	}
	return resp, decodeRecords(t, rec.Body.Bytes())
}

// idsOf lists the records' ids in reply order.
func idsOf(records []map[string]any) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		id, _ := r["id"].(string)
		out = append(out, id)
	}
	return out
}

// sortedIds lists the records' ids sorted.
func sortedIds(records []map[string]any) []string {
	out := idsOf(records)
	slices.Sort(out)
	return out
}

// sortedOf returns ids sorted.
func sortedOf(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// TestServer_SharedDatasetDeclare pins how a shared dataset and its
// declared indexes read back — the type's dataset list, the parts view
// and space discovery all carry `shared` and the indexes — and that
// `shared` is pinned and refused on a module dataset.
func TestServer_SharedDatasetDeclare(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)

	checkDefs := func(t *testing.T, defs []api.DatasetDefResponse) {
		t.Helper()
		byKey := map[string]api.DatasetDefResponse{}
		for _, def := range defs {
			byKey[def.Key] = def
		}
		if len(byKey) != 3 {
			t.Fatalf("defs = %+v", defs)
		}
		samples, events, notes := byKey["samples"], byKey["events"], byKey["notes"]
		if !samples.Shared || samples.Collection != l.samples || samples.Module != "records" ||
			samples.IdRule != "user" || samples.PartId != l.partId || samples.Invalid {
			t.Errorf("samples = %+v", samples)
		}
		if len(samples.Indexes) != 1 {
			t.Fatalf("samples indexes = %+v", samples.Indexes)
		}
		if x := samples.Indexes[0]; x.Id == "" || x.Key != "by_kind" ||
			!slices.Equal(x.Fields, []string{"kind", "-score", "_objectId"}) || x.Sparse || x.Invalid {
			t.Errorf("samples index = %+v", x)
		}
		if !events.Shared || events.Collection != l.events || events.IdRule != "auto" || len(events.Indexes) != 0 {
			t.Errorf("events = %+v", events)
		}
		if notes.Shared || notes.Collection != l.notes || len(notes.Indexes) != 1 {
			t.Fatalf("notes = %+v", notes)
		}
		if x := notes.Indexes[0]; x.Id == "" || x.Key != "top" || !slices.Equal(x.Fields, []string{"-score"}) ||
			!x.Sparse || x.Invalid {
			t.Errorf("notes index = %+v", x)
		}
	}

	t.Run("type datasets", func(t *testing.T) {
		checkDefs(t, l.datasetDefs(t, e))
		// A per-object dataset carries no `shared` key at all.
		rec := doJSON(t, e, http.MethodGet, l.base()+"/types/"+l.typeId+"/datasets", "")
		var raw struct {
			Datasets []map[string]json.RawMessage `json:"datasets"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		for _, def := range raw.Datasets {
			_, has := def["shared"]
			if key := string(def["key"]); has != (key != `"notes"`) {
				t.Errorf("dataset %s: shared key present = %v", key, has)
			}
		}
	})

	t.Run("parts view", func(t *testing.T) {
		var parts api.TypePartsListResponse
		decodeGet(t, e, l.base()+"/types/"+l.typeId+"/parts", &parts)
		if len(parts.Parts) != 1 || parts.Parts[0].Id != l.partId {
			t.Fatalf("parts = %+v", parts.Parts)
		}
		checkDefs(t, parts.Parts[0].Datasets)
	})

	t.Run("discovery", func(t *testing.T) {
		samples := l.discovered(t, e, l.samples)
		if !samples.Shared || samples.Module != "records" || !slices.Equal(samples.Owners, []string{l.typeId}) {
			t.Errorf("samples row = %+v", samples)
		}
		if len(samples.Indexes) != 1 || samples.Indexes[0].Key != "by_kind" ||
			!slices.Equal(samples.Indexes[0].Fields, []string{"kind", "-score", "_objectId"}) || samples.Indexes[0].Sparse {
			t.Errorf("samples indexes = %+v", samples.Indexes)
		}
		if events := l.discovered(t, e, l.events); !events.Shared || len(events.Indexes) != 0 {
			t.Errorf("events row = %+v", events)
		}
		notes := l.discovered(t, e, l.notes)
		if notes.Shared || len(notes.Indexes) != 1 || notes.Indexes[0].Key != "top" ||
			!slices.Equal(notes.Indexes[0].Fields, []string{"-score"}) || !notes.Indexes[0].Sparse {
			t.Errorf("notes row = %+v", notes)
		}
	})

	t.Run("patch refuses shared", func(t *testing.T) {
		for key, body := range map[string]string{
			"samples": `{"set":{"shared":false}}`,
			"notes":   `{"set":{"shared":true}}`,
		} {
			rec := doJSON(t, e, http.MethodPatch, l.base()+"/types/"+l.typeId+"/datasets/"+l.defs[key], body)
			assertStatusCode(t, rec, http.StatusBadRequest, "dataset.immutable")
		}
		checkDefs(t, l.datasetDefs(t, e))
	})

	t.Run("module dataset refuses shared", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, l.base()+"/types/"+l.typeId+"/parts",
			`{"key":"doc","datasets":[{"module":"editor","shared":true}]}`)
		assertStatusCode(t, rec, http.StatusBadRequest, "dataset.decl_invalid")
		rec = doJSON(t, e, http.MethodPost, l.base()+"/types/"+l.typeId+"/parts/"+l.partId+"/datasets",
			`{"key":"body","module":"editor","shared":true}`)
		assertStatusCode(t, rec, http.StatusBadRequest, "dataset.decl_invalid")
		if defs := l.datasetDefs(t, e); len(defs) != 3 {
			t.Errorf("a refused declaration landed: %+v", defs)
		}
	})
}

// TestServer_SharedDatasetWrites pins the per-object write and read
// routes on a shared dataset: records read back as
// `<objectId>/<recordId>` with `_objectId`, a write takes the plain id
// or the request object's own composite id, and another object's id is
// refused on modify and delete-records and rejected per record on
// upsert.
func TestServer_SharedDatasetWrites(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	a, b, c := l.objs[0], l.objs[1], l.objs[2]

	t.Run("modify returns composite ids", func(t *testing.T) {
		for _, r := range l.seedRows() {
			res := l.put(t, e, r.obj, l.samples, r.id, map[string]any{"label": r.label, "score": r.score, "kind": r.kind})
			if !slices.Equal(res.RecordIds, []string{sharedId(r.obj, r.id)}) || res.VersionId == "" {
				t.Errorf("modify %s on %s = %+v", r.id, r.obj, res)
			}
		}
		for _, obj := range l.objs {
			if res := l.put(t, e, obj, l.notes, "r1", map[string]any{"label": "note", "score": 1}); !slices.Equal(res.RecordIds, []string{"r1"}) {
				t.Errorf("per-object modify on %s = %+v", obj, res)
			}
		}
		// A derived id is prefixed too; the plain part never holds `/`.
		res := l.put(t, e, a, l.events, "", map[string]any{"label": "started"})
		if len(res.RecordIds) != 1 {
			t.Fatalf("derived id result = %+v", res)
		}
		plain, ok := strings.CutPrefix(res.RecordIds[0], a+"/")
		if !ok || plain == "" || strings.Contains(plain, "/") {
			t.Errorf("derived id = %q, want %s/<recordId>", res.RecordIds[0], a)
		}
		_, recs := postQuery(t, e, l.base()+"/query", `{"objectId":"`+a+`","dataset":"`+l.events+`"}`)
		if !slices.Equal(idsOf(recs), res.RecordIds) {
			t.Errorf("events of %s = %v, want %v", a, idsOf(recs), res.RecordIds)
		}
	})

	t.Run("per-object query reads one object", func(t *testing.T) {
		want := map[string][]string{
			a: {sharedId(a, "r1"), sharedId(a, "r2")},
			b: {sharedId(b, "r1"), sharedId(b, "r2")},
			c: {sharedId(c, "r1")},
		}
		for obj, ids := range want {
			_, recs := postQuery(t, e, l.base()+"/query", `{"objectId":"`+obj+`","dataset":"`+l.samples+`","sort":["id"]}`)
			if !slices.Equal(idsOf(recs), ids) {
				t.Errorf("records of %s = %v, want %v", obj, idsOf(recs), ids)
			}
			for _, r := range recs {
				if r["_objectId"] != obj {
					t.Errorf("record %v: _objectId = %v, want %s", r["id"], r["_objectId"], obj)
				}
			}
		}
		// A per-object dataset keeps its plain ids and has no _objectId.
		_, recs := postQuery(t, e, l.base()+"/query", `{"objectId":"`+a+`","dataset":"`+l.notes+`"}`)
		if !slices.Equal(idsOf(recs), []string{"r1"}) || recs[0]["_objectId"] != nil {
			t.Errorf("notes of %s = %+v", a, recs)
		}
	})

	label := func(t *testing.T, obj, id string) any {
		t.Helper()
		_, recs := postQuery(t, e, l.base()+"/query",
			`{"objectId":"`+obj+`","dataset":"`+l.samples+`","filter":{"id":"`+sharedId(obj, id)+`"}}`)
		if len(recs) != 1 {
			t.Fatalf("record %s of %s: %+v", id, obj, recs)
		}
		return recs[0]["label"]
	}

	t.Run("modify by plain and own composite id", func(t *testing.T) {
		res := l.set(t, e, a, l.samples, "r1", "label", "a1 plain")
		if !slices.Equal(res.RecordIds, []string{sharedId(a, "r1")}) {
			t.Errorf("plain id result = %+v", res)
		}
		if got := label(t, a, "r1"); got != "a1 plain" {
			t.Errorf("label after plain-id write = %v", got)
		}
		res = l.set(t, e, a, l.samples, sharedId(a, "r1"), "label", "a1 composite")
		if !slices.Equal(res.RecordIds, []string{sharedId(a, "r1")}) {
			t.Errorf("composite id result = %+v", res)
		}
		if got := label(t, a, "r1"); got != "a1 composite" {
			t.Errorf("label after composite-id write = %v", got)
		}
		// The same plain id on another object is another record.
		if got := label(t, b, "r1"); got != "b1" {
			t.Errorf("label of %s/r1 = %v, want b1", b, got)
		}
	})

	t.Run("modify refuses another object's id", func(t *testing.T) {
		for _, id := range []string{sharedId(b, "r1"), "x/r1"} {
			rec := doJSON(t, e, http.MethodPost, l.base()+"/modify", setBody(a, l.samples, id, "label", "stolen"))
			assertStatusCode(t, rec, http.StatusBadRequest, "record.wrong_object")
			apiErr := decodeErrEnvelope(t, rec.Body.Bytes())
			if apiErr.Details["objectId"] != a || apiErr.Details["dataset"] != l.samples {
				t.Errorf("%s: details = %v", id, apiErr.Details)
			}
		}
		if got := label(t, b, "r1"); got != "b1" {
			t.Errorf("label of %s/r1 = %v after a refused write", b, got)
		}
	})

	t.Run("delete-records", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, l.base()+"/delete-records",
			`{"objectId":"`+a+`","dataset":"`+l.samples+`","recordIds":["`+sharedId(b, "r2")+`"]}`)
		assertStatusCode(t, rec, http.StatusBadRequest, "record.wrong_object")
		if apiErr := decodeErrEnvelope(t, rec.Body.Bytes()); apiErr.Details["objectId"] != a || apiErr.Details["dataset"] != l.samples {
			t.Errorf("details = %v", apiErr.Details)
		}
		mustModify(t, e, http.MethodPost, l.base()+"/delete-records",
			`{"objectId":"`+b+`","dataset":"`+l.samples+`","recordIds":["`+sharedId(b, "r2")+`"]}`, http.StatusOK)
		mustModify(t, e, http.MethodPost, l.base()+"/delete-records",
			`{"objectId":"`+c+`","dataset":"`+l.samples+`","recordIds":["r1"]}`, http.StatusOK)
		_, recs := postQuery(t, e, l.base()+"/query", `{"objectId":"`+b+`","dataset":"`+l.samples+`"}`)
		if !slices.Equal(idsOf(recs), []string{sharedId(b, "r1")}) {
			t.Errorf("records of %s = %v", b, idsOf(recs))
		}
		_, recs = postQuery(t, e, l.base()+"/query", `{"objectId":"`+c+`","dataset":"`+l.samples+`"}`)
		if len(recs) != 0 {
			t.Errorf("records of %s = %v, want none", c, idsOf(recs))
		}
		// The first object's records survive the refused delete.
		_, recs = postQuery(t, e, l.base()+"/query", `{"objectId":"`+a+`","dataset":"`+l.samples+`"}`)
		if len(recs) != 2 {
			t.Errorf("records of %s = %v", a, idsOf(recs))
		}
	})

	t.Run("upsert", func(t *testing.T) {
		upsert := func(t *testing.T, body string) api.UpsertResult {
			t.Helper()
			rec := doJSON(t, e, http.MethodPost, l.base()+"/upsert", body)
			if rec.Code != http.StatusOK {
				t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
			}
			var out api.UpsertResult
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
		create := `{"objectId":"` + a + `","dataset":"` + l.samples + `","records":[
			{"id":"u1","fields":{"label":"u1","score":1,"kind":"x"}},
			{"id":"u2","fields":{"label":"u2","score":2,"kind":"y"}}]}`
		out := upsert(t, create)
		if out.Created != 2 || out.Updated != 0 || len(out.Rejections) != 0 || len(out.Pages) != 1 ||
			!slices.Equal(sortedOf(out.Pages[0].RecordIds...), sortedOf(sharedId(a, "u1"), sharedId(a, "u2"))) {
			t.Fatalf("create = %+v", out)
		}
		if rerun := upsert(t, create); rerun.Created != 0 || rerun.Updated != 0 || rerun.Skipped != 2 || len(rerun.Rejections) != 0 {
			t.Errorf("identical rerun = %+v", rerun)
		}

		// The own composite id names the plain-id record; another
		// object's id is a per-record rejection.
		mixed := `{"objectId":"` + a + `","dataset":"` + l.samples + `","records":[
			{"id":"` + sharedId(a, "u1") + `","fields":{"label":"u1 edited","score":1,"kind":"x"}},
			{"id":"u2","fields":{"label":"u2","score":2,"kind":"y"}},
			{"id":"` + sharedId(b, "r1") + `","fields":{"label":"stolen","score":1,"kind":"x"}}]}`
		out = upsert(t, mixed)
		if out.Created != 0 || out.Updated != 1 || out.Skipped != 1 {
			t.Errorf("mixed counters = %+v", out)
		}
		if len(out.Rejections) != 1 || out.Rejections[0].Index != 2 || out.Rejections[0].Id != sharedId(b, "r1") ||
			out.Rejections[0].Code != "upsert.rejected" {
			t.Errorf("mixed rejections = %+v", out.Rejections)
		}
		rerun := upsert(t, mixed)
		if rerun.Created != 0 || rerun.Updated != 0 || rerun.Skipped != 2 || len(rerun.Rejections) != 1 ||
			rerun.Rejections[0].Id != sharedId(b, "r1") {
			t.Errorf("mixed rerun = %+v", rerun)
		}
		// A rejection of the plain record reports the plain id.
		out = upsert(t, `{"objectId":"`+a+`","dataset":"`+l.samples+`","records":[
			{"id":"`+sharedId(a, "u2")+`","fields":{"label":"u2","score":2,"kind":"z"}}]}`)
		if len(out.Rejections) != 1 || out.Rejections[0].Id != "u2" || out.Rejections[0].Code != "upsert.immutable_field" {
			t.Errorf("immutable change by composite id = %+v", out)
		}

		_, recs := postQuery(t, e, l.base()+"/query", `{"objectId":"`+a+`","dataset":"`+l.samples+`","sort":["id"]}`)
		want := []string{sharedId(a, "r1"), sharedId(a, "r2"), sharedId(a, "u1"), sharedId(a, "u2")}
		if !slices.Equal(idsOf(recs), want) {
			t.Fatalf("records of %s = %v, want %v", a, idsOf(recs), want)
		}
		if recs[2]["label"] != "u1 edited" {
			t.Errorf("u1 = %+v", recs[2])
		}
		if got := label(t, b, "r1"); got != "b1" {
			t.Errorf("label of %s/r1 = %v after a rejected upsert", b, got)
		}
	})
}

// TestServer_SharedDatasetQuery drives POST …/datasets/query: every
// object's records under composite ids, filters on a field and on
// `_objectId`, sort and paging with totals, projection, and tombstones
// left out.
func TestServer_SharedDatasetQuery(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	l.seed(t, e)
	a, b, c := l.objs[0], l.objs[1], l.objs[2]
	path := l.base() + "/datasets/query"
	all := []string{sharedId(a, "r1"), sharedId(a, "r2"), sharedId(b, "r1"), sharedId(b, "r2"), sharedId(c, "r1")}

	t.Run("every object's records", func(t *testing.T) {
		resp, recs := postQuery(t, e, path, `{"dataset":"`+l.samples+`"}`)
		if !slices.Equal(sortedIds(recs), sortedOf(all...)) {
			t.Fatalf("ids = %v, want %v", idsOf(recs), all)
		}
		if resp.Total != nil || resp.HasNext != nil {
			t.Errorf("total/hasNext without includeTotal: %v %v", resp.Total, resp.HasNext)
		}
		for _, r := range recs {
			obj, _ := r["_objectId"].(string)
			if id, _ := r["id"].(string); obj == "" || !strings.HasPrefix(id, obj+"/") {
				t.Errorf("record %v names object %q", r["id"], obj)
			}
			if r["kind"] == nil || r["label"] == nil || r["score"] == nil {
				t.Errorf("record %v lost a field: %+v", r["id"], r)
			}
		}
	})

	t.Run("filter", func(t *testing.T) {
		_, recs := postQuery(t, e, path, `{"dataset":"`+l.samples+`","filter":{"kind":"x"}}`)
		if want := sortedOf(sharedId(a, "r1"), sharedId(b, "r1"), sharedId(c, "r1")); !slices.Equal(sortedIds(recs), want) {
			t.Errorf("kind x = %v, want %v", idsOf(recs), want)
		}
		_, recs = postQuery(t, e, path, `{"dataset":"`+l.samples+`","filter":{"_objectId":"`+b+`"}}`)
		if want := sortedOf(sharedId(b, "r1"), sharedId(b, "r2")); !slices.Equal(sortedIds(recs), want) {
			t.Errorf("_objectId %s = %v, want %v", b, idsOf(recs), want)
		}
		_, recs = postQuery(t, e, path, `{"dataset":"`+l.samples+`","filter":{"score":{"$gte":20},"_objectId":{"$ne":"`+b+`"}}}`)
		if want := sortedOf(sharedId(a, "r2"), sharedId(c, "r1")); !slices.Equal(sortedIds(recs), want) {
			t.Errorf("score >= 20 off %s = %v, want %v", b, idsOf(recs), want)
		}
		rec := doJSON(t, e, http.MethodPost, path, `{"dataset":"`+l.samples+`","filter":{"kind":"none"}}`)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); rec.Code != http.StatusOK || err != nil || string(raw["records"]) != "[]" {
			t.Errorf("no match: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("sort and paging", func(t *testing.T) {
		_, recs := postQuery(t, e, path, `{"dataset":"`+l.samples+`","sort":["-score"]}`)
		desc := []string{sharedId(c, "r1"), sharedId(b, "r2"), sharedId(b, "r1"), sharedId(a, "r2"), sharedId(a, "r1")}
		if !slices.Equal(idsOf(recs), desc) {
			t.Fatalf("-score = %v, want %v", idsOf(recs), desc)
		}
		resp, recs := postQuery(t, e, path, `{"dataset":"`+l.samples+`","sort":["-score"],"limit":2,"offset":1,"includeTotal":true}`)
		if !slices.Equal(idsOf(recs), desc[1:3]) {
			t.Errorf("page = %v, want %v", idsOf(recs), desc[1:3])
		}
		if resp.Total == nil || *resp.Total != 5 || resp.HasNext == nil || !*resp.HasNext {
			t.Errorf("page total=%v hasNext=%v, want 5 true", resp.Total, resp.HasNext)
		}
		resp, recs = postQuery(t, e, path, `{"dataset":"`+l.samples+`","sort":["-score"],"limit":2,"offset":4,"includeTotal":true}`)
		if !slices.Equal(idsOf(recs), desc[4:]) || resp.Total == nil || *resp.Total != 5 || resp.HasNext == nil || *resp.HasNext {
			t.Errorf("last page = %v total=%v hasNext=%v", idsOf(recs), resp.Total, resp.HasNext)
		}
		resp, recs = postQuery(t, e, path, `{"dataset":"`+l.samples+`","filter":{"kind":"y"},"sort":["score"],"limit":1,"includeTotal":true}`)
		if !slices.Equal(idsOf(recs), []string{sharedId(a, "r2")}) || resp.Total == nil || *resp.Total != 2 || !*resp.HasNext {
			t.Errorf("filtered page = %v total=%v hasNext=%v", idsOf(recs), resp.Total, resp.HasNext)
		}
	})

	t.Run("projection", func(t *testing.T) {
		keys := func(r map[string]any) []string {
			out := make([]string, 0, len(r))
			for k := range r {
				out = append(out, k)
			}
			slices.Sort(out)
			return out
		}
		// Include mode keeps id, drops _objectId unless named.
		_, recs := postQuery(t, e, path, `{"dataset":"`+l.samples+`","projection":{"label":1}}`)
		if len(recs) != 5 {
			t.Fatalf("records = %v", idsOf(recs))
		}
		for _, r := range recs {
			if got := keys(r); !slices.Equal(got, []string{"_ver", "id", "label"}) {
				t.Errorf("include label: keys = %v", got)
			}
		}
		_, recs = postQuery(t, e, path, `{"dataset":"`+l.samples+`","projection":{"label":1,"_objectId":1}}`)
		for _, r := range recs {
			obj, _ := r["_objectId"].(string)
			if id, _ := r["id"].(string); obj == "" || !strings.HasPrefix(id, obj+"/") || r["score"] != nil {
				t.Errorf("include label,_objectId: %+v", r)
			}
		}
		// Exclude mode keeps _objectId.
		_, recs = postQuery(t, e, path, `{"dataset":"`+l.samples+`","projection":{"label":-1}}`)
		for _, r := range recs {
			if r["label"] != nil || r["_objectId"] == nil || r["score"] == nil || r["id"] == nil {
				t.Errorf("exclude label: %+v", r)
			}
		}
	})

	t.Run("tombstones are left out", func(t *testing.T) {
		mustModify(t, e, http.MethodPost, l.base()+"/delete-records",
			`{"objectId":"`+a+`","dataset":"`+l.samples+`","recordIds":["r2"]}`, http.StatusOK)
		resp, recs := postQuery(t, e, path, `{"dataset":"`+l.samples+`","includeTotal":true}`)
		want := sortedOf(sharedId(a, "r1"), sharedId(b, "r1"), sharedId(b, "r2"), sharedId(c, "r1"))
		if !slices.Equal(sortedIds(recs), want) || resp.Total == nil || *resp.Total != 4 {
			t.Errorf("after delete = %v total=%v, want %v", idsOf(recs), resp.Total, want)
		}
		_, recs = postQuery(t, e, path, `{"dataset":"`+l.samples+`","filter":{"_objectId":"`+a+`"}}`)
		if !slices.Equal(idsOf(recs), []string{sharedId(a, "r1")}) {
			t.Errorf("records of %s = %v", a, idsOf(recs))
		}
	})

	t.Run("a shared dataset nothing wrote reads empty", func(t *testing.T) {
		resp, recs := postQuery(t, e, path, `{"dataset":"`+l.events+`","includeTotal":true}`)
		if len(recs) != 0 || resp.Total == nil || *resp.Total != 0 {
			t.Errorf("events = %v total=%v", idsOf(recs), resp.Total)
		}
	})

	// The per-object read of a shared dataset counts, pages and lists
	// tombstones of its own object only.
	t.Run("per-object totals, pages and tombstones", func(t *testing.T) {
		resp, recs := postQuery(t, e, l.base()+"/query",
			`{"objectId":"`+b+`","dataset":"`+l.samples+`","sort":["-score"],"limit":1,"includeTotal":true}`)
		if !slices.Equal(idsOf(recs), []string{sharedId(b, "r2")}) || resp.Total == nil || *resp.Total != 2 || !*resp.HasNext {
			t.Errorf("page of %s = %v total=%v hasNext=%v", b, idsOf(recs), resp.Total, resp.HasNext)
		}
		resp, recs = postQuery(t, e, l.base()+"/query",
			`{"objectId":"`+a+`","dataset":"`+l.samples+`","includeDeleted":true,"sort":["id"],"includeTotal":true}`)
		if !slices.Equal(idsOf(recs), []string{sharedId(a, "r1"), sharedId(a, "r2")}) || resp.Total == nil || *resp.Total != 2 {
			t.Fatalf("records of %s with tombstones = %v total=%v", a, idsOf(recs), resp.Total)
		}
		if recs[0]["_deletedAt"] != nil || recs[1]["_deletedAt"] == nil {
			t.Errorf("tombstone marker: %+v", recs)
		}
		_, recs = postQuery(t, e, l.base()+"/query",
			`{"objectId":"`+c+`","dataset":"`+l.samples+`","includeDeleted":true}`)
		if !slices.Equal(idsOf(recs), []string{sharedId(c, "r1")}) {
			t.Errorf("records of %s with tombstones = %v", c, idsOf(recs))
		}
	})
}

// TestServer_SharedDatasetAggregate drives POST …/datasets/aggregate:
// a group by `_objectId` agrees with each object's own aggregate, a
// $match spans objects, explain returns a plan, tombstones are left
// out.
func TestServer_SharedDatasetAggregate(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	l.seed(t, e)
	a, b, c := l.objs[0], l.objs[1], l.objs[2]
	path := l.base() + "/datasets/aggregate"

	type group struct {
		Id    string  `json:"id"`
		N     int     `json:"n"`
		Total float64 `json:"total"`
	}
	groups := func(t *testing.T, out *api.AggregateResponse) []group {
		t.Helper()
		var gs []group
		for _, raw := range out.Records {
			var g group
			if err := json.Unmarshal(raw, &g); err != nil {
				t.Fatalf("decode group: %v (%s)", err, raw)
			}
			gs = append(gs, g)
		}
		return gs
	}
	count := func(t *testing.T, pipeline string) int {
		t.Helper()
		out := aggDo(t, e, path, `{"dataset":"`+l.samples+`","pipeline":`+pipeline+`}`)
		if len(out.Records) != 1 {
			t.Fatalf("%s: records = %s", pipeline, out.Records)
		}
		var row struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal(out.Records[0], &row); err != nil {
			t.Fatal(err)
		}
		return row.N
	}
	const byObject = `[{"$group":{"_id":"$_objectId","n":{"$count":{}},"total":{"$sum":"$score"}}},{"$sort":{"id":1}}]`

	t.Run("group by object", func(t *testing.T) {
		got := groups(t, aggDo(t, e, path, `{"dataset":"`+l.samples+`","pipeline":`+byObject+`}`))
		want := []group{{a, 2, 30}, {b, 2, 70}, {c, 1, 50}}
		slices.SortFunc(want, func(x, y group) int { return strings.Compare(x.Id, y.Id) })
		if !slices.Equal(got, want) {
			t.Fatalf("groups = %+v, want %+v", got, want)
		}
		// Each group equals that object's own aggregate.
		for _, g := range want {
			own := groups(t, aggDo(t, e, l.base()+"/aggregate",
				`{"objectId":"`+g.Id+`","dataset":"`+l.samples+`","pipeline":`+byObject+`}`))
			if !slices.Equal(own, []group{g}) {
				t.Errorf("own aggregate of %s = %+v, want %+v", g.Id, own, g)
			}
		}
	})

	t.Run("match and count", func(t *testing.T) {
		if n := count(t, `[{"$match":{"kind":"x"}},{"$count":"n"}]`); n != 3 {
			t.Errorf("kind x = %d, want 3", n)
		}
		if n := count(t, `[{"$match":{"_objectId":"`+b+`"}},{"$count":"n"}]`); n != 2 {
			t.Errorf("_objectId %s = %d, want 2", b, n)
		}
		if n := count(t, `[{"$count":"n"}]`); n != 5 {
			t.Errorf("all = %d, want 5", n)
		}
	})

	t.Run("explain", func(t *testing.T) {
		out := aggDo(t, e, path, `{"dataset":"`+l.samples+`","explain":true,"pipeline":`+byObject+`}`)
		if out.Plan == nil || *out.Plan == "" || len(out.Records) != 0 {
			t.Errorf("explain = %+v", out)
		}
	})

	t.Run("tombstones are left out", func(t *testing.T) {
		mustModify(t, e, http.MethodPost, l.base()+"/delete-records",
			`{"objectId":"`+b+`","dataset":"`+l.samples+`","recordIds":["r2"]}`, http.StatusOK)
		got := groups(t, aggDo(t, e, path, `{"dataset":"`+l.samples+`","pipeline":`+byObject+`}`))
		want := []group{{a, 2, 30}, {b, 1, 30}, {c, 1, 50}}
		slices.SortFunc(want, func(x, y group) int { return strings.Compare(x.Id, y.Id) })
		if !slices.Equal(got, want) {
			t.Errorf("groups after delete = %+v, want %+v", got, want)
		}
		if n := count(t, `[{"$match":{"kind":"y"}},{"$count":"n"}]`); n != 1 {
			t.Errorf("kind y after delete = %d, want 1", n)
		}
	})
}

// openStream opens a subscribe stream with open (a client Stream*
// method) and returns its frames. The stream ends with ctx; wait on the
// returned channel before the server closes.
func openStream(ctx context.Context, open func(context.Context, string, []byte, func(client.SSEFrame) error) error,
	spaceId, body string) (<-chan client.SSEFrame, <-chan error) {
	frames := make(chan client.SSEFrame, 64)
	done := make(chan error, 1)
	go func() {
		done <- open(ctx, spaceId, []byte(body), func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-ctx.Done():
			}
			return nil
		})
	}()
	return frames, done
}

// readSnapshot reads the ready and snapshot frames a stream opens with.
func readSnapshot(t *testing.T, frames <-chan client.SSEFrame) api.QuerySubscribeSnapshot {
	t.Helper()
	if f := waitFrame(t, frames, 5*time.Second); f.Event != "ready" {
		t.Fatalf("first frame = %q (%s), want ready", f.Event, f.Data)
	}
	f := waitFrame(t, frames, 5*time.Second)
	if f.Event != "snapshot" {
		t.Fatalf("second frame = %q (%s), want snapshot", f.Event, f.Data)
	}
	var snap api.QuerySubscribeSnapshot
	if err := json.Unmarshal(f.Data, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snap
}

// collectChanges reads `changes` frames until done holds for the
// events gathered so far, and returns them. Other frames are skipped;
// a `closed` frame fails the test, as does a 5s timeout.
func collectChanges(t *testing.T, frames <-chan client.SSEFrame, done func([]api.QuerySubscribeEvent) bool) []api.QuerySubscribeEvent {
	t.Helper()
	var evs []api.QuerySubscribeEvent
	deadline := time.After(5 * time.Second)
	for !done(evs) {
		select {
		case f := <-frames:
			switch f.Event {
			case "closed":
				t.Fatalf("stream closed: %s", f.Data)
			case "changes":
				var batch []api.QuerySubscribeEvent
				if err := json.Unmarshal(f.Data, &batch); err != nil {
					t.Fatalf("decode changes: %v (%s)", err, f.Data)
				}
				evs = append(evs, batch...)
			}
		case <-deadline:
			t.Fatalf("timed out; events so far: %+v", evs)
		}
	}
	return evs
}

// changeIds lists the record ids the events carry, every bucket.
func changeIds(evs []api.QuerySubscribeEvent) []string {
	var out []string
	for _, ev := range evs {
		for _, r := range ev.Added {
			out = append(out, r.Id)
		}
		for _, r := range ev.Updated {
			out = append(out, r.Id)
		}
		for _, r := range ev.Removed {
			out = append(out, r.Id)
		}
	}
	return out
}

// changeDoc returns the doc of id's entry in bucket ("added" or
// "updated"), nil when no event carries one.
func changeDoc(evs []api.QuerySubscribeEvent, bucket, id string) map[string]any {
	for _, ev := range evs {
		recs := ev.Added
		if bucket == "updated" {
			recs = ev.Updated
		}
		for _, r := range recs {
			if r.Id == id {
				var doc map[string]any
				if err := json.Unmarshal(r.Doc, &doc); err != nil {
					return nil
				}
				return doc
			}
		}
	}
	return nil
}

// removal returns the reason and batch version id of id's removal, ok
// false when no event carries one.
func removal(evs []api.QuerySubscribeEvent, id string) (reason, versionId string, ok bool) {
	for _, ev := range evs {
		for _, r := range ev.Removed {
			if r.Id == id {
				return r.Reason, ev.VersionId, true
			}
		}
	}
	return "", "", false
}

// TestServer_SharedDatasetSubscribe drives POST
// …/datasets/query/subscribe: the snapshot spans every object, a create
// on one object, an update on another and a record delete arrive as
// added / updated / removed, deleting an object removes its records in
// batches with an empty versionId, a projection shapes the changes, and
// a filtered stream — or one object's own stream — stays silent for
// writes outside it.
func TestServer_SharedDatasetSubscribe(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	l := setupSharedLab(t, e)
	l.seed(t, e)
	a, b, c := l.objs[0], l.objs[1], l.objs[2]

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	streamCtx, streamCancel := context.WithCancel(ctx)
	all, allDone := openStream(streamCtx, cl.StreamDatasetQuerySubscribe, l.spaceId,
		`{"dataset":"`+l.samples+`","sort":["score","id"],"limit":50,"includeTotal":true}`)
	kindY, kindYDone := openStream(streamCtx, cl.StreamDatasetQuerySubscribe, l.spaceId,
		`{"dataset":"`+l.samples+`","filter":{"kind":"y"},"sort":["score"],"limit":50}`)
	labels, labelsDone := openStream(streamCtx, cl.StreamDatasetQuerySubscribe, l.spaceId,
		`{"dataset":"`+l.samples+`","projection":{"label":1}}`)
	ownC, ownCDone := openStream(streamCtx, cl.StreamQuerySubscribe, l.spaceId,
		`{"objectId":"`+c+`","dataset":"`+l.samples+`"}`)
	defer func() {
		streamCancel()
		for _, done := range []<-chan error{allDone, kindYDone, labelsDone, ownCDone} {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("stream did not exit after cancel")
			}
		}
	}()

	snap := readSnapshot(t, all)
	snapIds := make([]string, 0, len(snap.Records))
	for _, raw := range snap.Records {
		var r struct {
			Id       string `json:"id"`
			ObjectId string `json:"_objectId"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(r.Id, r.ObjectId+"/") {
			t.Errorf("snapshot record %s names object %q", r.Id, r.ObjectId)
		}
		snapIds = append(snapIds, r.Id)
	}
	want := []string{sharedId(a, "r1"), sharedId(a, "r2"), sharedId(b, "r1"), sharedId(b, "r2"), sharedId(c, "r1")}
	if !slices.Equal(snapIds, want) {
		t.Fatalf("snapshot = %v, want %v", snapIds, want)
	}
	if snap.Total == nil || *snap.Total != 5 || snap.HasNext == nil || *snap.HasNext {
		t.Errorf("snapshot total=%v hasNext=%v", snap.Total, snap.HasNext)
	}
	ySnap := readSnapshot(t, kindY)
	if len(ySnap.Records) != 2 {
		t.Fatalf("kind y snapshot = %s", ySnap.Records)
	}
	lSnap := readSnapshot(t, labels)
	if len(lSnap.Records) != 5 || strings.Contains(string(lSnap.Records[0]), "_objectId") {
		t.Fatalf("projected snapshot = %s", lSnap.Records)
	}
	if cSnap := readSnapshot(t, ownC); len(cSnap.Records) != 1 || !strings.Contains(string(cSnap.Records[0]), `"`+sharedId(c, "r1")+`"`) {
		t.Fatalf("snapshot of %s = %s", c, cSnap.Records)
	}

	only := func(t *testing.T, evs []api.QuerySubscribeEvent, ids ...string) {
		t.Helper()
		for _, id := range changeIds(evs) {
			if !slices.Contains(ids, id) {
				t.Errorf("unexpected change for %s in %+v", id, evs)
			}
		}
	}

	t.Run("create on one object", func(t *testing.T) {
		l.put(t, e, a, l.samples, "r3", map[string]any{"label": "a3", "score": 60, "kind": "x"})
		id := sharedId(a, "r3")
		evs := collectChanges(t, all, func(evs []api.QuerySubscribeEvent) bool { return changeDoc(evs, "added", id) != nil })
		only(t, evs, id)
		if doc := changeDoc(evs, "added", id); doc["_objectId"] != a || doc["label"] != "a3" {
			t.Errorf("added doc = %+v", doc)
		}
		// Include mode drops _objectId from the change too.
		evs = collectChanges(t, labels, func(evs []api.QuerySubscribeEvent) bool { return changeDoc(evs, "added", id) != nil })
		only(t, evs, id)
		if doc := changeDoc(evs, "added", id); doc["label"] != "a3" || doc["id"] != id || doc["_objectId"] != nil || doc["score"] != nil {
			t.Errorf("projected added doc = %+v", doc)
		}
	})

	t.Run("update on another object", func(t *testing.T) {
		l.set(t, e, b, l.samples, "r1", "label", "b1 edited")
		id := sharedId(b, "r1")
		evs := collectChanges(t, all, func(evs []api.QuerySubscribeEvent) bool { return changeDoc(evs, "updated", id) != nil })
		only(t, evs, id)
		if doc := changeDoc(evs, "updated", id); doc["_objectId"] != b || doc["label"] != "b1 edited" {
			t.Errorf("updated doc = %+v", doc)
		}
	})

	t.Run("record delete", func(t *testing.T) {
		mustModify(t, e, http.MethodPost, l.base()+"/delete-records",
			`{"objectId":"`+c+`","dataset":"`+l.samples+`","recordIds":["r1"]}`, http.StatusOK)
		id := sharedId(c, "r1")
		evs := collectChanges(t, all, func(evs []api.QuerySubscribeEvent) bool { _, _, ok := removal(evs, id); return ok })
		only(t, evs, id)
		if reason, _, _ := removal(evs, id); reason != "deleted" {
			t.Errorf("removal reason = %q", reason)
		}
		// The object's own stream skipped the writes on the others.
		evs = collectChanges(t, ownC, func(evs []api.QuerySubscribeEvent) bool { _, _, ok := removal(evs, id); return ok })
		only(t, evs, id)
	})

	t.Run("filtered stream stays silent", func(t *testing.T) {
		// The three writes above are outside the filter: the first frame
		// the filtered stream delivers is this matching create.
		l.put(t, e, c, l.samples, "r2", map[string]any{"label": "c2", "score": 70, "kind": "y"})
		id := sharedId(c, "r2")
		evs := collectChanges(t, kindY, func(evs []api.QuerySubscribeEvent) bool { return changeDoc(evs, "added", id) != nil })
		only(t, evs, id)
		collectChanges(t, all, func(evs []api.QuerySubscribeEvent) bool { return changeDoc(evs, "added", id) != nil })
	})

	t.Run("object delete", func(t *testing.T) {
		doJSONExpect(t, e, http.MethodDelete, l.base()+"/objects/"+b, http.StatusNoContent)
		gone := []string{sharedId(b, "r1"), sharedId(b, "r2")}
		evs := collectChanges(t, all, func(evs []api.QuerySubscribeEvent) bool {
			for _, id := range gone {
				if _, _, ok := removal(evs, id); !ok {
					return false
				}
			}
			return true
		})
		only(t, evs, gone...)
		for _, id := range gone {
			if reason, versionId, _ := removal(evs, id); reason != "deleted" || versionId != "" {
				t.Errorf("removal of %s: reason %q versionId %q, want deleted and empty", id, reason, versionId)
			}
		}
		yEvs := collectChanges(t, kindY, func(evs []api.QuerySubscribeEvent) bool { _, _, ok := removal(evs, gone[1]); return ok })
		only(t, yEvs, gone[1])

		_, recs := postQuery(t, e, l.base()+"/datasets/query", `{"dataset":"`+l.samples+`","filter":{"_objectId":"`+b+`"}}`)
		if len(recs) != 0 {
			t.Errorf("records of the deleted object = %v", idsOf(recs))
		}
	})
}

// TestServer_SharedDatasetErrors pins the refusals of the dataset-scope
// reads: a missing dataset, an unknown body key, a dataset that is not
// shared or not held by the space, the key-exchange dataset, the tech
// space and an unordered live window.
func TestServer_SharedDatasetErrors(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	l.seed(t, e)
	other := mustCreateSpace(t, e, "SharedLabOther")
	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)

	query, sub, agg := l.base()+"/datasets/query", l.base()+"/datasets/query/subscribe", l.base()+"/datasets/aggregate"
	reads := func(dataset string) []struct{ path, body string } {
		return []struct{ path, body string }{
			{query, `{"dataset":"` + dataset + `"}`},
			{sub, `{"dataset":"` + dataset + `"}`},
			{agg, `{"dataset":"` + dataset + `","pipeline":[{"$count":"n"}]}`},
			{agg, `{"dataset":"` + dataset + `","explain":true,"pipeline":[{"$count":"n"}]}`},
		}
	}
	type refusal struct {
		name, path, body string
		status           int
		code             string
	}
	var cases []refusal
	for _, c := range []struct{ path, body string }{
		{query, `{}`}, {query, `{"filter":{"kind":"x"}}`}, {sub, `{}`}, {agg, `{"pipeline":[]}`},
	} {
		cases = append(cases, refusal{"no dataset", c.path, c.body, http.StatusBadRequest, "request.missing_field"})
	}
	cases = append(cases, refusal{"no pipeline", agg, `{"dataset":"` + l.samples + `"}`, http.StatusBadRequest, "request.missing_field"})
	for _, c := range []struct{ path, body string }{
		{query, `{"dataset":"` + l.samples + `","objectId":"` + l.objs[0] + `"}`},
		{query, `{"dataset":"` + l.samples + `","includeDeleted":true}`},
		{query, `{"dataset":"` + l.samples + `","filters":{"kind":"x"}}`},
		{sub, `{"dataset":"` + l.samples + `","objectId":"` + l.objs[0] + `"}`},
	} {
		cases = append(cases, refusal{"unknown key", c.path, c.body, http.StatusBadRequest, "request.unknown_field"})
	}
	for _, ds := range []struct{ name, dataset string }{
		{"per-object dataset", l.notes}, {"unknown dataset", "nope"},
		{"objects", "objects"}, {"module dataset", "editor_blocks"},
	} {
		for _, c := range reads(ds.dataset) {
			cases = append(cases, refusal{"not shared: " + ds.name, c.path, c.body, http.StatusBadRequest, "dataset.not_shared"})
		}
	}
	for _, c := range reads(l.samples) {
		path := strings.Replace(c.path, l.spaceId, other, 1)
		cases = append(cases, refusal{"another space", path, c.body, http.StatusBadRequest, "dataset.not_shared"})
	}
	for _, c := range reads("identityKeys") {
		cases = append(cases, refusal{"identityKeys", c.path, c.body, http.StatusBadRequest, "request.invalid_field"})
	}
	for _, c := range reads(l.samples) {
		path := strings.Replace(c.path, l.spaceId, acc.TechSpaceId, 1)
		cases = append(cases, refusal{"tech space", path, c.body, http.StatusMethodNotAllowed, "space.unsupported"})
	}
	for _, c := range reads(l.samples) {
		path := strings.Replace(c.path, l.spaceId, "nope", 1)
		cases = append(cases, refusal{"unknown space", path, c.body, http.StatusNotFound, "space.not_found"})
	}
	cases = append(cases,
		refusal{"limit without sort", sub, `{"dataset":"` + l.samples + `","limit":2}`, http.StatusBadRequest, "request.invalid_field"},
		refusal{"bad filter", query, `{"dataset":"` + l.samples + `","filter":{"kind":{"$nope":1}}}`, http.StatusBadRequest, "filter.unknown_operator"},
		refusal{"bad pipeline", agg, `{"dataset":"` + l.samples + `","pipeline":[{"$bogus":{}}]}`, http.StatusBadRequest, "aggregate.bad_pipeline"},
	)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, c.path, c.body)
			assertStatusCode(t, rec, c.status, c.code)
		})
	}

	t.Run("not shared names the dataset", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, query, `{"dataset":"`+l.notes+`"}`)
		if apiErr := decodeErrEnvelope(t, rec.Body.Bytes()); apiErr.Details["dataset"] != l.notes {
			t.Errorf("details = %v", apiErr.Details)
		}
	})

	t.Run("a per-object dataset still reads per object", func(t *testing.T) {
		_, recs := postQuery(t, e, l.base()+"/query", `{"objectId":"`+l.objs[0]+`","dataset":"`+l.notes+`"}`)
		if !slices.Equal(idsOf(recs), []string{"r1"}) {
			t.Errorf("notes = %v", idsOf(recs))
		}
	})
}

// TestServer_SharedDatasetSearch: a searchable shared dataset is
// indexed, and its search hits and link sources name the record by its
// plain id next to the object and the dataset.
func TestServer_SharedDatasetSearch(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil)
	defer func() { _ = ix.Close() }()

	spaceId, typeId, objA := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId
	mustAddPart(t, e, spaceId, typeId, `{"key":"clips","datasets":[{"key":"clips","shared":true,"idRule":"user",
		"search":{"title":"title","text":"body"},
		"fields":[{"key":"title","kind":"string","mutableBy":"any"},
			{"key":"body","kind":"string","mutableBy":"any","xFormat":{"type":"markdown"}}]}]}`)
	objB := mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`"}`)
	target := mustCreateObject(t, e, spaceId, `{"type":"page","initialProperties":{"any":{"name":"target"}}}`)
	targetUri := "any://o/" + spaceId + "/" + target
	clips := typeId + "_clips"
	mustModify(t, e, http.MethodPost, base+"/modify",
		putBody(objA, clips, "r1", map[string]any{"title": "Glacier retreat", "body": "annual mass balance, see [target](" + targetUri + ")"}), http.StatusOK)
	// B's record cites A's record by the plain record id.
	recordUri := "any://o/" + spaceId + "/" + objA + "/" + clips + "/r1"
	mustModify(t, e, http.MethodPost, base+"/modify",
		putBody(objB, clips, "r1", map[string]any{"title": "Tidal power", "body": "estuary turbine deployment, after [the survey](" + recordUri + ")"}), http.StatusOK)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	for q, obj := range map[string]string{"glacier": objA, "turbine": objB} {
		res := doSearch(t, e, spaceId, api.SearchRequest{Query: q, Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
		if len(res.Hits) != 1 {
			t.Fatalf("%s: hits = %+v", q, res.Hits)
		}
		if h := res.Hits[0]; h.RecordId != "r1" || h.ObjectId != obj || h.Dataset != clips || h.Scope != "basic" {
			t.Errorf("%s: hit = %+v, want record r1 of %s in %s", q, h, obj, clips)
		}
	}
	bl := getBacklinks(t, e, spaceId, target, "")
	if len(bl.Object) != 1 {
		t.Fatalf("backlinks = %+v", bl)
	}
	if src := bl.Object[0].Source; src.RecordId != "r1" || src.ObjectId != objA || src.Dataset != clips || src.Field != "body" {
		t.Errorf("link source = %+v, want record r1 of %s in %s", src, objA, clips)
	}
	narrowed := getBacklinks(t, e, spaceId, objA, "?record=r1&dataset="+clips)
	if len(narrowed.Object) != 1 {
		t.Fatalf("backlinks of %s's record r1 = %+v", objA, narrowed)
	}
	if l := narrowed.Object[0]; l.Source.RecordId != "r1" || l.Source.ObjectId != objB || l.Source.Dataset != clips ||
		l.Target.Uri != recordUri || l.Target.ObjectId != objA || l.Target.RecordId != "r1" {
		t.Errorf("record link = %+v", l)
	}

	// A delete by the composite id evicts the record's entry and edge.
	mustModify(t, e, http.MethodPost, base+"/delete-records",
		`{"objectId":"`+objA+`","dataset":"`+clips+`","recordIds":["`+sharedId(objA, "r1")+`"]}`, http.StatusOK)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if res := doSearch(t, e, spaceId, api.SearchRequest{Query: "glacier", Mode: api.SearchModeFTS}, http.StatusOK); len(res.Hits) != 0 {
		t.Errorf("deleted record still indexed: %+v", res.Hits)
	}
	if res := doSearch(t, e, spaceId, api.SearchRequest{Query: "turbine", Mode: api.SearchModeFTS}, http.StatusOK); len(res.Hits) != 1 {
		t.Errorf("the other object's record went too: %+v", res.Hits)
	}
	if bl := getBacklinks(t, e, spaceId, target, ""); len(bl.Object) != 0 {
		t.Errorf("deleted record's edge survives: %+v", bl.Object)
	}
	if bl := getBacklinks(t, e, spaceId, objA, "?record=r1&dataset="+clips); len(bl.Object) != 1 {
		t.Errorf("the other record's edge went too: %+v", bl)
	}
}

// TestServer_SharedDatasetHistory: version history of a shared
// dataset's record — the one-record read takes the plain record id and
// returns the record under its composite id; the change list and the
// diff name it by the composite id and filter by the plain one.
func TestServer_SharedDatasetHistory(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	l.seed(t, e)
	a := l.objs[0]
	hist := l.base() + "/objects/" + a + "/history"
	edit := l.set(t, e, a, l.samples, "r1", "label", "a1 edited")

	var list api.HistoryListResponse
	decodeGet(t, e, hist+"?dataset="+l.samples+"&recordId=r1", &list)
	if len(list.Changes) != 2 || list.Changes[0].Version != edit.ChangeId {
		t.Fatalf("changes of r1 = %+v", list.Changes)
	}
	for _, ch := range list.Changes {
		if len(ch.Touched) != 1 || ch.Touched[0].RecordId != sharedId(a, "r1") || ch.Touched[0].Dataset != l.samples {
			t.Errorf("touched = %+v", ch.Touched)
		}
	}

	var at api.HistoryRecordResponse
	decodeGet(t, e, hist+"/"+edit.ChangeId+"/datasets/"+l.samples+"/records/r1", &at)
	var row map[string]any
	if err := json.Unmarshal(at.Record, &row); err != nil {
		t.Fatalf("decode record: %v (%s)", err, at.Record)
	}
	if !at.Exists || at.Deleted || at.RecordId != "r1" || row["id"] != sharedId(a, "r1") ||
		row["_objectId"] != a || row["label"] != "a1 edited" {
		t.Errorf("record at the edit = %+v %v", at, row)
	}
	decodeGet(t, e, hist+"/"+list.Changes[1].Version+"/datasets/"+l.samples+"/records/r1", &at)
	if err := json.Unmarshal(at.Record, &row); err != nil || !at.Exists || row["label"] != "a1" {
		t.Errorf("record at its creation = %+v %v", at, row)
	}
	// The record's own id, percent-encoded, reads the same record.
	at = api.HistoryRecordResponse{}
	decodeGet(t, e, hist+"/"+edit.ChangeId+"/datasets/"+l.samples+"/records/"+url.PathEscape(sharedId(a, "r1")), &at)
	if err := json.Unmarshal(at.Record, &row); err != nil || !at.Exists || row["label"] != "a1 edited" {
		t.Errorf("record by its percent-encoded id = %+v %v", at, row)
	}

	var diff api.HistoryDiffResponse
	decodeGet(t, e, hist+"/diff?version="+edit.ChangeId+"&dataset="+l.samples+"&recordIds=r1", &diff)
	if len(diff.Datasets) != 1 || len(diff.Datasets[0].Records) != 1 ||
		diff.Datasets[0].Records[0].Id != sharedId(a, "r1") || diff.Datasets[0].Records[0].Kind != "changed" {
		t.Errorf("diff = %+v", diff)
	}
}

// TestServer_HistoryRecordIdEscapes: the one-record history route reads
// the record the path names — a percent sign in a record id is decoded
// once, whether or not the path also carries an escaped slash.
func TestServer_HistoryRecordIdEscapes(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	mustAddPart(t, e, l.spaceId, l.typeId, `{"key":"codes","datasets":[
		{"key":"codes","idRule":"user","idPattern":"[a-zA-Z0-9%]+","fields":[
			{"key":"label","kind":"string","mutableBy":"any"}]}]}`)
	codes := l.typeId + "_codes"
	a := l.objs[0]
	l.put(t, e, a, codes, "a%41", map[string]any{"label": "percent"})
	last := l.put(t, e, a, codes, "aA", map[string]any{"label": "letter"})

	for _, tc := range []struct{ segment, id, label string }{
		{"a%2541", "a%41", "percent"},
		{"aA", "aA", "letter"},
	} {
		var at api.HistoryRecordResponse
		decodeGet(t, e, l.base()+"/objects/"+a+"/history/"+last.ChangeId+"/datasets/"+codes+"/records/"+tc.segment, &at)
		var row map[string]any
		if err := json.Unmarshal(at.Record, &row); err != nil || !at.Exists || at.RecordId != tc.id || row["label"] != tc.label {
			t.Errorf("records/%s = %+v %v", tc.segment, at, row)
		}
	}
}

// TestServer_SharedDatasetAggregateBodyIsClosed: the dataset-scope
// aggregate refuses a key its body does not take — an `objectId` there
// would read as a pipeline over one object.
func TestServer_SharedDatasetAggregateBodyIsClosed(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	for _, key := range []string{"objectId", "filter"} {
		body, _ := json.Marshal(map[string]any{"dataset": l.samples, "pipeline": []any{}, key: l.objs[0]})
		rec := doJSON(t, e, http.MethodPost, l.base()+"/datasets/aggregate", string(body))
		assertStatusCode(t, rec, http.StatusBadRequest, "request.unknown_field")
	}
}
