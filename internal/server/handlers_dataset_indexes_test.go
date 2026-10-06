package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/indexer"
)

// indexesPath is the declared-index route of the lab dataset key.
func (l *sharedLab) indexesPath(key string) string {
	return l.base() + "/types/" + l.typeId + "/datasets/" + l.defs[key] + "/indexes"
}

// addIndex declares an index on the lab dataset key and returns its id.
func (l *sharedLab) addIndex(t *testing.T, e http.Handler, key, body string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, l.indexesPath(key), body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add index %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var out api.AddDatasetIndexResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.IndexDefId == "" {
		t.Fatalf("decode index: %v %s", err, rec.Body.String())
	}
	return out.IndexDefId
}

// indexKeys lists the keys of listed indexes in order.
func indexKeys(indexes []api.DatasetIndexDef) []string {
	out := make([]string, 0, len(indexes))
	for _, x := range indexes {
		out = append(out, x.Key)
	}
	return out
}

// discoveredKeys lists the keys of discovered indexes in order.
func discoveredKeys(indexes []api.DatasetIndexDraft) []string {
	out := make([]string, 0, len(indexes))
	for _, x := range indexes {
		out = append(out, x.Key)
	}
	return out
}

// planOf POSTs an explain body and returns the plan.
func planOf(t *testing.T, e http.Handler, path, body string) string {
	t.Helper()
	out := aggDo(t, e, path, body)
	if out.Plan == nil || *out.Plan == "" {
		t.Fatalf("POST %s %s: no plan: %+v", path, body, out)
	}
	return *out.Plan
}

// pollPlan polls an explain body until the plan does (named) or does
// not (!named) name the any-store index, and returns the plan. Fails
// after 10s with the last plan.
func pollPlan(t *testing.T, e http.Handler, path, body, index string, named bool) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		plan := planOf(t, e, path, body)
		if strings.Contains(plan, index) == named {
			return plan
		}
		if time.Now().After(deadline) {
			t.Fatalf("plan never had %s named=%v; last plan:\n%s", index, named, plan)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fillSamples upserts n samples records on each lab object: ids s000…,
// score object*1000 + i, kind alternating x / y, label l<i>.
func (l *sharedLab) fillSamples(t *testing.T, e http.Handler, n int) {
	t.Helper()
	for o, obj := range l.objs {
		recs := make([]string, 0, n)
		for i := range n {
			recs = append(recs, fmt.Sprintf(`{"id":"s%03d","fields":{"label":"l%d","score":%d,"kind":%q}}`,
				i, i, o*1000+i, []string{"x", "y"}[i%2]))
		}
		rec := doJSON(t, e, http.MethodPost, l.base()+"/upsert",
			`{"objectId":"`+obj+`","dataset":"`+l.samples+`","records":[`+strings.Join(recs, ",")+`]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
		}
		var out api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Created != n || len(out.Rejections) != 0 {
			t.Fatalf("upsert on %s: %v %+v", obj, err, out)
		}
	}
}

// TestServer_DatasetIndexShared declares indexes on a shared dataset
// that already holds records: the definition lists at once, the build
// runs in the background until explain names the any-store index, query
// results are the same before and after, and a per-object read stays on
// its object. Removing the index takes it out of the listings and the
// plan.
func TestServer_DatasetIndexShared(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	l.fillSamples(t, e, 100)

	aggPath := l.base() + "/datasets/aggregate"
	explainScore := `{"dataset":"` + l.samples + `","explain":true,"pipeline":[{"$match":{"score":{"$gte":2090}}}]}`
	explainLabel := `{"dataset":"` + l.samples + `","explain":true,"pipeline":[{"$match":{"label":"l7"}}]}`
	const byScore, byLabel = "dx_score,_objectId", "dx_label~sparse"
	queries := []string{
		`{"dataset":"` + l.samples + `","filter":{"score":{"$gte":2090}},"sort":["score"],"includeTotal":true}`,
		`{"dataset":"` + l.samples + `","filter":{"score":{"$gte":1050,"$lt":2050}},"sort":["-score"],"limit":20,"offset":5,"includeTotal":true}`,
		`{"dataset":"` + l.samples + `","filter":{"label":"l7"},"sort":["_objectId"],"includeTotal":true}`,
	}
	type answer struct {
		ids   []string
		total int
	}
	answers := func(t *testing.T) []answer {
		t.Helper()
		var out []answer
		for _, q := range queries {
			resp, recs := postQuery(t, e, l.base()+"/datasets/query", q)
			if resp.Total == nil {
				t.Fatalf("%s: no total", q)
			}
			out = append(out, answer{idsOf(recs), *resp.Total})
		}
		return out
	}
	sameAnswers := func(t *testing.T, got, want []answer) {
		t.Helper()
		for i := range want {
			if !slices.Equal(got[i].ids, want[i].ids) || got[i].total != want[i].total {
				t.Errorf("%s: %v (total %d), want %v (total %d)", queries[i], got[i].ids, got[i].total, want[i].ids, want[i].total)
			}
		}
	}

	before := answers(t)
	if before[0].total != 10 || before[1].total != 100 || len(before[1].ids) != 20 || before[2].total != 3 {
		t.Fatalf("baseline answers = %+v", before)
	}
	for body, index := range map[string]string{explainScore: byScore, explainLabel: byLabel} {
		if plan := planOf(t, e, aggPath, body); strings.Contains(plan, index) {
			t.Fatalf("plan names %s before it is declared:\n%s", index, plan)
		}
	}

	var scoreId, labelId string
	t.Run("add", func(t *testing.T) {
		scoreId = l.addIndex(t, e, "samples", `{"key":"by_score","fields":["score","_objectId"]}`)
		labelId = l.addIndex(t, e, "samples", `{"key":"by_label","fields":["label"],"sparse":true}`)

		def := l.def(t, e, "samples")
		if got := indexKeys(def.Indexes); !slices.Equal(got, []string{"by_kind", "by_score", "by_label"}) {
			t.Fatalf("listed indexes = %v", got)
		}
		if x := def.Indexes[1]; x.Id != scoreId || !slices.Equal(x.Fields, []string{"score", "_objectId"}) || x.Sparse || x.Invalid {
			t.Errorf("by_score = %+v", x)
		}
		if x := def.Indexes[2]; x.Id != labelId || !slices.Equal(x.Fields, []string{"label"}) || !x.Sparse || x.Invalid {
			t.Errorf("by_label = %+v", x)
		}
		disc := l.discovered(t, e, l.samples)
		if got := discoveredKeys(disc.Indexes); !slices.Equal(got, []string{"by_kind", "by_score", "by_label"}) {
			t.Fatalf("discovered indexes = %v", got)
		}
		if x := disc.Indexes[2]; !slices.Equal(x.Fields, []string{"label"}) || !x.Sparse {
			t.Errorf("discovered by_label = %+v", x)
		}
	})

	t.Run("explain names the built index", func(t *testing.T) {
		pollPlan(t, e, aggPath, explainScore, byScore, true)
		pollPlan(t, e, aggPath, explainLabel, byLabel, true)
		sameAnswers(t, answers(t), before)
	})

	// An index read keeps a per-object read on its own object.
	t.Run("per-object reads under an index", func(t *testing.T) {
		for o, want := range []int{0, 0, 10} {
			obj := l.objs[o]
			resp, recs := postQuery(t, e, l.base()+"/query",
				`{"objectId":"`+obj+`","dataset":"`+l.samples+`","filter":{"score":{"$gte":2090}},"includeTotal":true}`)
			if len(recs) != want || resp.Total == nil || *resp.Total != want {
				t.Errorf("%s: %v total=%v, want %d", obj, idsOf(recs), resp.Total, want)
			}
			for _, r := range recs {
				if r["_objectId"] != obj {
					t.Errorf("%s: read %v", obj, r["id"])
				}
			}
			out := aggDo(t, e, l.base()+"/aggregate",
				`{"objectId":"`+obj+`","dataset":"`+l.samples+`","pipeline":[{"$match":{"label":"l7"}},{"$count":"n"}]}`)
			if n := len(out.Records); n != 1 || !strings.Contains(string(out.Records[0]), `"n":1`) {
				t.Errorf("%s: label count = %s", obj, out.Records)
			}
		}
	})

	t.Run("writes after the build", func(t *testing.T) {
		l.put(t, e, l.objs[0], l.samples, "late", map[string]any{"label": "l7", "score": 2500, "kind": "x"})
		resp, recs := postQuery(t, e, l.base()+"/datasets/query", queries[0])
		if resp.Total == nil || *resp.Total != 11 || idsOf(recs)[10] != sharedId(l.objs[0], "late") {
			t.Errorf("after a write: %v total=%v", idsOf(recs), resp.Total)
		}
		mustModify(t, e, http.MethodPost, l.base()+"/delete-records",
			`{"objectId":"`+l.objs[0]+`","dataset":"`+l.samples+`","recordIds":["late"]}`, http.StatusOK)
		sameAnswers(t, answers(t), before)
	})

	t.Run("remove", func(t *testing.T) {
		doJSONExpect(t, e, http.MethodDelete, l.indexesPath("samples")+"/"+scoreId, http.StatusNoContent)
		if got := indexKeys(l.def(t, e, "samples").Indexes); !slices.Equal(got, []string{"by_kind", "by_label"}) {
			t.Errorf("listed after remove = %v", got)
		}
		if got := discoveredKeys(l.discovered(t, e, l.samples).Indexes); !slices.Equal(got, []string{"by_kind", "by_label"}) {
			t.Errorf("discovered after remove = %v", got)
		}
		pollPlan(t, e, aggPath, explainScore, byScore, false)
		if plan := planOf(t, e, aggPath, explainLabel); !strings.Contains(plan, byLabel) {
			t.Errorf("the other index left the plan:\n%s", plan)
		}
		sameAnswers(t, answers(t), before)

		rec := doJSON(t, e, http.MethodDelete, l.indexesPath("samples")+"/"+scoreId, "")
		assertStatusCode(t, rec, http.StatusNotFound, "sdk.not_found")
	})
}

// TestServer_DatasetIndexPerObject: a per-object dataset's index is
// built on each object's collection when that object's records are
// next written, and leaves it the same way.
func TestServer_DatasetIndexPerObject(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)
	a, b := l.objs[0], l.objs[1]
	aggPath := l.base() + "/aggregate"
	explain := func(obj, match string) string {
		return `{"objectId":"` + obj + `","dataset":"` + l.notes + `","explain":true,"pipeline":[{"$match":` + match + `}]}`
	}

	// The index declared with the dataset.
	l.put(t, e, a, l.notes, "n1", map[string]any{"label": "first", "score": 5})
	if plan := planOf(t, e, aggPath, explain(a, `{"score":{"$gte":1}}`)); !strings.Contains(plan, "dx_-score~sparse") {
		t.Errorf("declared index missing from the plan:\n%s", plan)
	}

	// An index added later.
	labelId := l.addIndex(t, e, "notes", `{"key":"by_label","fields":["label","_ver.id"]}`)
	if got := indexKeys(l.def(t, e, "notes").Indexes); !slices.Equal(got, []string{"top", "by_label"}) {
		t.Fatalf("listed indexes = %v", got)
	}
	if got := discoveredKeys(l.discovered(t, e, l.notes).Indexes); !slices.Equal(got, []string{"top", "by_label"}) {
		t.Errorf("discovered indexes = %v", got)
	}
	const byLabel = "dx_label,_ver.id"
	for _, obj := range []string{a, b} {
		l.put(t, e, obj, l.notes, "n2", map[string]any{"label": "second", "score": 6})
		if plan := planOf(t, e, aggPath, explain(obj, `{"label":"second"}`)); !strings.Contains(plan, byLabel) {
			t.Errorf("%s: added index missing from the plan after a write:\n%s", obj, plan)
		}
		_, recs := postQuery(t, e, l.base()+"/query", `{"objectId":"`+obj+`","dataset":"`+l.notes+`","filter":{"label":"second"}}`)
		if !slices.Equal(idsOf(recs), []string{"n2"}) {
			t.Errorf("%s: label query = %v", obj, idsOf(recs))
		}
	}

	doJSONExpect(t, e, http.MethodDelete, l.indexesPath("notes")+"/"+labelId, http.StatusNoContent)
	if got := indexKeys(l.def(t, e, "notes").Indexes); !slices.Equal(got, []string{"top"}) {
		t.Errorf("listed after remove = %v", got)
	}
	l.put(t, e, a, l.notes, "n3", map[string]any{"label": "third", "score": 7})
	if plan := planOf(t, e, aggPath, explain(a, `{"label":"second"}`)); strings.Contains(plan, byLabel) {
		t.Errorf("removed index still in the plan after a write:\n%s", plan)
	}
}

// TestServer_DatasetIndexRefusals pins every refused index declaration
// and removal, and that none of them changes the listed set.
func TestServer_DatasetIndexRefusals(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	l := setupSharedLab(t, e)

	samples, notes := l.indexesPath("samples"), l.indexesPath("notes")
	cases := []struct {
		name, path, body string
		status           int
		code             string
	}{
		{"unknown field", samples, `{"key":"a","fields":["nope"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"array field", samples, `{"key":"a","fields":["tags"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"object field", samples, `{"key":"a","fields":["meta"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"_objectId on a per-object dataset", notes, `{"key":"a","fields":["_objectId"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"taken key", samples, `{"key":"by_kind","fields":["label"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"five fields", samples, `{"key":"a","fields":["label","score","kind","_objectId","_ver.id"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"empty fields", samples, `{"key":"a","fields":[]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"no fields", samples, `{"key":"a"}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"a field twice", samples, `{"key":"a","fields":["label","-label"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"non-slug key", samples, `{"key":"Not A Slug","fields":["score"]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"unknown body key", samples, `{"key":"a","fields":["score"],"unique":true}`, http.StatusBadRequest, "request.unknown_field"},
		{"unknown defId", l.base() + "/types/" + l.typeId + "/datasets/dsdnope/indexes", `{"key":"a","fields":["score"]}`, http.StatusNotFound, "sdk.not_found"},
		{"not a type", l.base() + "/types/" + l.objs[0] + "/datasets/" + l.defs["samples"] + "/indexes", `{"key":"a","fields":["score"]}`, http.StatusNotFound, "type.not_found"},
		{"draft with a bad index", l.base() + "/types/" + l.typeId + "/parts/" + l.partId + "/datasets",
			`{"key":"bad","fields":[{"key":"x","kind":"string"}],"indexes":[{"key":"a","fields":["y"]}]}`, http.StatusBadRequest, "dataset.decl_invalid"},
		{"part draft with a bad index", l.base() + "/types/" + l.typeId + "/parts",
			`{"key":"bad","datasets":[{"key":"bad","fields":[{"key":"x","kind":"string"}],"indexes":[{"key":"a","fields":["_objectId"]}]}]}`, http.StatusBadRequest, "dataset.decl_invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertStatusCode(t, doJSON(t, e, http.MethodPost, c.path, c.body), c.status, c.code)
		})
	}

	t.Run("unknown indexId", func(t *testing.T) {
		assertStatusCode(t, doJSON(t, e, http.MethodDelete, samples+"/nope", ""), http.StatusNotFound, "sdk.not_found")
	})

	t.Run("a field an index names", func(t *testing.T) {
		var scoreField string
		for _, f := range l.def(t, e, "samples").Fields {
			if f.Key == "score" {
				scoreField = f.Id
			}
		}
		rec := doJSON(t, e, http.MethodDelete, l.base()+"/types/"+l.typeId+"/datasets/"+l.defs["samples"]+"/fields/"+scoreField, "")
		assertStatusCode(t, rec, http.StatusBadRequest, "dataset.decl_invalid")
		if !slices.ContainsFunc(l.def(t, e, "samples").Fields, func(f api.DatasetFieldDef) bool { return f.Key == "score" }) {
			t.Error("the indexed field was removed")
		}
	})

	t.Run("module dataset", func(t *testing.T) {
		edType := installModuleType(t, e, l.spaceId, "editor")
		var list api.TypeDatasetsListResponse
		decodeGet(t, e, l.base()+"/types/"+edType+"/datasets", &list)
		if len(list.Datasets) != 1 || list.Datasets[0].Module != "editor" {
			t.Fatalf("editor type datasets = %+v", list.Datasets)
		}
		rec := doJSON(t, e, http.MethodPost, l.base()+"/types/"+edType+"/datasets/"+list.Datasets[0].Id+"/indexes",
			`{"key":"a","fields":["text"]}`)
		assertStatusCode(t, rec, http.StatusConflict, "dataset.module_owned")
	})

	t.Run("nothing landed", func(t *testing.T) {
		if got := indexKeys(l.def(t, e, "samples").Indexes); !slices.Equal(got, []string{"by_kind"}) {
			t.Errorf("samples indexes = %v", got)
		}
		if got := indexKeys(l.def(t, e, "notes").Indexes); !slices.Equal(got, []string{"top"}) {
			t.Errorf("notes indexes = %v", got)
		}
		for _, def := range l.datasetDefs(t, e) {
			if def.Key == "bad" {
				t.Errorf("a refused dataset landed: %+v", def)
			}
		}
	})

	t.Run("a ninth index", func(t *testing.T) {
		// Seven more accepted shapes fill the dataset to eight; two of
		// one shape are two definitions.
		for i, fields := range []string{
			`["_ver.id"]`, `["-_objectId","label"]`, `["label","score","kind","_objectId"]`,
			`["-label"]`, `["kind","_ver.id"]`, `["score"]`, `["score"]`,
		} {
			l.addIndex(t, e, "samples", fmt.Sprintf(`{"key":"k%d","fields":%s}`, i, fields))
		}
		rec := doJSON(t, e, http.MethodPost, samples, `{"key":"k7","fields":["label"]}`)
		assertStatusCode(t, rec, http.StatusBadRequest, "dataset.decl_invalid")
		got := l.def(t, e, "samples").Indexes
		if len(got) != 8 {
			t.Fatalf("listed indexes = %v", indexKeys(got))
		}
		for _, x := range got {
			if x.Invalid {
				t.Errorf("index %s listed invalid: %s", x.Key, x.InvalidReason)
			}
		}
		if disc := l.discovered(t, e, l.samples).Indexes; len(disc) != 8 {
			t.Errorf("discovered indexes = %v", discoveredKeys(disc))
		}
	})
}

// TestServer_DatasetIndexProcess: building a shared dataset's index
// shows on the process view as dataset.index.<spaceId>, kind
// dataset.index, target the storage collection — a started event, then
// done — and the row lists on GET /v1/processes.
func TestServer_DatasetIndexProcess(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := indexer.OpenStoreInMemory(ctx, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(d.sdk, d.eng.chunkers, st, indexer.Options{
		Debounce:  10 * time.Millisecond,
		OnProcess: d.indexerProcessFor(d.eng),
	})
	d.indexer = ix
	defer func() { _ = ix.Close() }()
	ix.Start(ctx)

	spaceId, typeId, objA := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId
	partId := mustAddPart(t, e, spaceId, typeId, `{"key":"lab"}`)
	rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets", `{
		"key": "readings", "shared": true, "idRule": "user", "search": {"title": "label"},
		"fields": [{"key": "label", "kind": "string"}, {"key": "score", "kind": "number"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dataset: %d %s", rec.Code, rec.Body.String())
	}
	var added api.AddDatasetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	readings := added.Collection
	for o, obj := range []string{objA, mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`"}`)} {
		recs := make([]string, 0, 200)
		for i := range 200 {
			recs = append(recs, fmt.Sprintf(`{"id":"r%d","fields":{"label":"reading %d","score":%d}}`, i, i, o*1000+i))
		}
		recs[0] = `{"id":"r0","fields":{"label":"calibration probe","score":0}}`
		rec := doJSON(t, e, http.MethodPost, base+"/upsert",
			`{"objectId":"`+obj+`","dataset":"`+readings+`","records":[`+strings.Join(recs, ",")+`]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
		}
	}
	// The space's indexer worker reports the builds: wait until it runs.
	pollSearch(t, e, spaceId, api.SearchRequest{Query: "calibration", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool { return len(r.Hits) == 2 }, "worker indexes the readings")

	procId := "dataset.index." + spaceId
	frames := make(chan client.SSEFrame, 32)
	go func() {
		_ = cl.StreamEvents(ctx, url.Values{"scope": {api.EventScopeDevice}, "type": {"process.*"}, "target": {procId}},
			func(f client.SSEFrame) error {
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

	rec = doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/datasets/"+added.DatasetDefId+"/indexes",
		`{"key":"by_score","fields":["score","_objectId"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add index: %d %s", rec.Code, rec.Body.String())
	}

	var types []string
	for !slices.Contains(types, api.EventProcessDone) && !slices.Contains(types, api.EventProcessFailed) {
		f := waitFrame(t, frames, 10*time.Second)
		if f.Event != "event" {
			continue
		}
		var ev api.Event
		if err := json.Unmarshal(f.Data, &ev); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		var data processEventData
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatalf("decode process data: %v", err)
		}
		// No counters: a build reports its start and its end only.
		if ev.Target != procId || ev.Scope != api.EventScopeDevice || data.Kind != "dataset.index" ||
			data.Target != readings || data.Title == "" || data.Total != 0 || ev.Sender == nil || !ev.Sender.Self {
			t.Errorf("event = %+v data = %+v", ev, data)
		}
		types = append(types, ev.Type)
	}
	// A heartbeat may fall between the two.
	types = slices.DeleteFunc(types, func(typ string) bool { return typ == api.EventProcessProgress })
	if !slices.Equal(types, []string{api.EventProcessStarted, api.EventProcessDone}) {
		t.Errorf("event types = %v, want started then done", types)
	}

	var list api.ProcessListResponse
	decodeGet(t, e, "/v1/processes", &list)
	i := slices.IndexFunc(list.Processes, func(p api.Process) bool { return p.Id == procId })
	if i < 0 {
		t.Fatalf("%s not on the process view: %+v", procId, list.Processes)
	}
	if p := list.Processes[i]; p.Kind != "dataset.index" || p.Target != readings || p.State != api.ProcessStateDone ||
		p.Scope != api.EventScopeDevice || !p.Self || p.Error != nil || p.Total != 0 {
		t.Errorf("process row = %+v", p)
	}

	// The build made the index the plan names.
	plan := planOf(t, e, base+"/datasets/aggregate",
		`{"dataset":"`+readings+`","explain":true,"pipeline":[{"$match":{"score":{"$gte":1190}}}]}`)
	if !strings.Contains(plan, "dx_score,_objectId") {
		t.Errorf("plan after the build:\n%s", plan)
	}
}
