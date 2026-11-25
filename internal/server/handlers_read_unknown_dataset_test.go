package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// TestServer_ReadUnknownDataset pins the read side of dataset.unknown:
// a per-object query, subscribe or aggregate naming a dataset the space
// does not have answers 400 with the name in details, as a write does
// (docs/06-errors.md). A dataset the space has reads as empty on an
// object with no records of it, and its live window fills when one
// lands.
func TestServer_ReadUnknownDataset(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	spaceId, typeId, objectId := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId
	partId := mustAddPart(t, e, spaceId, typeId, `{"key":"notes"}`)
	rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets",
		`{"key":"log","idRule":"user","fields":[{"key":"v","kind":"string","mutableBy":"any"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("declare dataset: %d %s", rec.Code, rec.Body.String())
	}
	declared := typeId + "_log"

	// An object of another type: the space has the dataset, the object's
	// type does not declare it.
	rec = doJSON(t, e, http.MethodPost, base+"/types", `{"name":"Other","xKey":"other"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var other api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &other); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	foreign := mustCreateObject(t, e, spaceId, `{"type":"`+other.TypeId+`"}`)

	reads := []struct{ route, extra string }{
		{"/query", ""},
		{"/query", `, "includeDeleted": true, "includeTotal": true`},
		{"/query/subscribe", ""},
		{"/query/subscribe", `, "sort": ["id"], "limit": 5`},
		{"/aggregate", `, "pipeline": [{"$count": "n"}]`},
		{"/aggregate", `, "pipeline": [{"$count": "n"}], "explain": true`},
	}
	body := func(objectId, dataset, extra string) string {
		return fmt.Sprintf(`{"objectId": %q, "dataset": %q%s}`, objectId, dataset, extra)
	}

	for _, dataset := range []string{"no-such-dataset", declared + "x", "log"} {
		for _, r := range reads {
			name := dataset + " " + r.route + r.extra
			rec := doJSON(t, e, http.MethodPost, base+r.route, body(objectId, dataset, r.extra))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: status=%d, want 400: %s", name, rec.Code, rec.Body.String())
				continue
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("%s: Content-Type = %q, want a JSON envelope", name, ct)
			}
			assertErrorCode(t, rec, "dataset.unknown")
			var env api.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("%s: decode envelope: %v", name, err)
			}
			if got, _ := env.Error.Details["dataset"].(string); got != dataset {
				t.Errorf("%s: details.dataset = %q, want %q (%s)", name, got, dataset, rec.Body.String())
			}
			if got, _ := env.Error.Details["objectId"].(string); got != objectId {
				t.Errorf("%s: details.objectId = %q, want %q (%s)", name, got, objectId, rec.Body.String())
			}
		}
	}

	// The object comes first: one the space does not have is not found,
	// whatever the dataset.
	const noObject = "bafyreigh2akiscaildcqabsyg3dfr6chu3fgpregiymsck7e7aqa4s52zy"
	for _, r := range []struct{ route, extra string }{reads[0], reads[4]} {
		rec := doJSON(t, e, http.MethodPost, base+r.route, body(noObject, "no-such-dataset", r.extra))
		if rec.Code != http.StatusNotFound {
			t.Errorf("unknown object %s: status=%d, want 404: %s", r.route, rec.Code, rec.Body.String())
			continue
		}
		assertErrorCode(t, rec, "object.not_found")
	}

	// A subscribe on such an object opens an empty window.
	absentCtx, absentCancel := context.WithTimeout(context.Background(), 30*time.Second)
	absent, absentDone := openStream(absentCtx, cl.StreamQuerySubscribe, spaceId, body(noObject, "no-such-dataset", ""))
	if snap := readSnapshot(t, absent); len(snap.Records) != 0 {
		t.Errorf("snapshot on an object the space does not have = %d records, want 0", len(snap.Records))
	}
	absentCancel()
	select {
	case <-absentDone:
	case <-time.After(5 * time.Second):
		t.Error("stream on an object the space does not have did not exit after cancel")
	}

	for name, oid := range map[string]string{"no records": objectId, "another type": foreign} {
		rec := doJSON(t, e, http.MethodPost, base+"/query", body(oid, declared, `, "includeTotal": true`))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: query: %d %s", name, rec.Code, rec.Body.String())
		}
		var out api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if len(out.Records) != 0 || out.Total == nil || *out.Total != 0 {
			t.Errorf("%s: query = %s, want no records and total 0", name, rec.Body.String())
		}
		rec = doJSON(t, e, http.MethodPost, base+"/aggregate", body(oid, declared, `, "pipeline": [{"$count": "n"}]`))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: aggregate: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	frames, done := openStream(ctx, cl.StreamQuerySubscribe, spaceId, body(objectId, declared, ""))
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("stream did not exit after cancel")
		}
	}()
	if snap := readSnapshot(t, frames); len(snap.Records) != 0 {
		t.Fatalf("snapshot of a dataset with no records = %d records, want 0", len(snap.Records))
	}
	rec = doJSON(t, e, http.MethodPost, base+"/modify", putBody(objectId, declared, "r1", map[string]any{"v": "late"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("modify: %d %s", rec.Code, rec.Body.String())
	}
	collectChanges(t, frames, func(evs []api.QuerySubscribeEvent) bool {
		for _, ev := range evs {
			for _, a := range ev.Added {
				if a.Id == "r1" {
					return true
				}
			}
		}
		return false
	})
}
