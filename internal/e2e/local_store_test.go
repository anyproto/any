// TestE2E_LocalStore pins the local store's on-disk contract against
// the real binary: local collections live in
// the SDK's sdk.db and survive a server restart (the SDK's boot-time
// orphan sweep leaves the l_ tag alone); a space-scoped collection
// outlives its space — deleting the space makes every write to it
// 409 space.deleted (the sticky tombstone) while reads, delete and
// drop keep working; and the fence refuses an SDK collection as a sink.
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

func TestE2E_LocalStore(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	waitForReady(t, addr, 30*time.Second)
	base := "http://" + addr

	spaceId := createSpace(t, base, "LocalStore", "")
	acc := `{"scope":"account","name":"scratch"}`
	sp := fmt.Sprintf(`{"scope":"space","spaceId":%q,"name":"cache"}`, spaceId)

	var ensured api.LocalEnsureResponse
	mustJSON(t, http.MethodPut, base+"/v1/local/collections", acc, http.StatusCreated, &ensured)
	if ensured.Collection.StorageName != "l_a_scratch" {
		t.Fatalf("storage name: %s", ensured.Collection.StorageName)
	}
	mustJSON(t, http.MethodPut, base+"/v1/local/collections", sp, http.StatusCreated, &ensured)
	if ensured.Collection.StorageName != "l_s_"+spaceId+"_cache" {
		t.Fatalf("space storage name: %s", ensured.Collection.StorageName)
	}
	mustStatus(t, http.MethodPost, base+"/v1/local/insert",
		`{"coll":`+acc+`,"docs":[{"id":"a","k":1},{"id":"b","k":2}]}`, http.StatusOK)
	mustStatus(t, http.MethodPost, base+"/v1/local/insert",
		`{"coll":`+sp+`,"docs":[{"id":"x","objectId":"obj-1"}]}`, http.StatusOK)

	// A rollup into another (ensured) local collection, and the fence
	// on an SDK one.
	mustStatus(t, http.MethodPut, base+"/v1/local/collections", `{"scope":"account","name":"rollup"}`, http.StatusCreated)
	var agg api.LocalAggregateResponse
	mustJSON(t, http.MethodPost, base+"/v1/local/aggregate",
		`{"coll":`+acc+`,"pipeline":[{"$group":{"_id":null,"sum":{"$sum":"$k"}}},{"$out":"l_a_rollup"}]}`,
		http.StatusOK, &agg)
	if agg.Written == nil || *agg.Written != 1 {
		t.Fatalf("$out written: %+v", agg)
	}
	var env map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/local/aggregate",
		`{"coll":`+acc+`,"pipeline":[{"$out":"`+spaceId+`_objects"}]}`, http.StatusBadRequest, &env)
	if code := env["error"].(map[string]any)["code"]; code != "local.bad_sink_target" {
		t.Fatalf("sink fence code = %v", code)
	}

	// The collections are in the SDK's own file, not a sidecar.
	if matches, _ := filepath.Glob(filepath.Join(dataDir, "*", "local")); len(matches) != 0 {
		t.Fatalf("unexpected sidecar dir: %v", matches)
	}
	if matches, _ := filepath.Glob(filepath.Join(dataDir, "*", "sdk", "sdk.db")); len(matches) != 1 {
		t.Fatalf("expected one sdk.db, got %v", matches)
	}

	t.Run("survive restart", func(t *testing.T) {
		srv.stop(t)
		srv = startServerInit(t, bin, addr, dataDir, false)
		waitForReady(t, addr, 30*time.Second)

		var list api.LocalListResponse
		mustJSON(t, http.MethodGet, base+"/v1/local/collections", "", http.StatusOK, &list)
		got := map[string]int{}
		for _, c := range list.Collections {
			got[c.StorageName] = c.Count
		}
		want := map[string]int{"l_a_scratch": 2, "l_a_rollup": 1, "l_s_" + spaceId + "_cache": 1}
		for name, n := range want {
			if got[name] != n {
				t.Errorf("after restart %s count=%d want %d (all: %v)", name, got[name], n, got)
			}
		}
		if len(got) != len(want) {
			t.Errorf("after restart collections: %v", got)
		}
		var rec api.LocalRecordResponse
		mustJSON(t, http.MethodPost, base+"/v1/local/get", `{"coll":`+acc+`,"id":"b"}`, http.StatusOK, &rec)
	})

	t.Run("space-scoped collection outlives its space", func(t *testing.T) {
		mustStatus(t, http.MethodDelete, base+"/v1/spaces/"+spaceId, "", http.StatusNoContent)

		// Writes are fenced by the space pre-flight: a dead space can't
		// be grown as a namespace.
		for _, w := range []struct{ method, path, body string }{
			{http.MethodPut, "/v1/local/collections", sp},
			{http.MethodPost, "/v1/local/insert", `{"coll":` + sp + `,"docs":[{"id":"y"}]}`},
			{http.MethodPost, "/v1/local/upsert", `{"coll":` + sp + `,"docs":[{"id":"x","objectId":"obj-2"}]}`},
		} {
			var env map[string]any
			mustJSON(t, w.method, base+w.path, w.body, http.StatusConflict, &env)
			if code := env["error"].(map[string]any)["code"]; code != "space.deleted" {
				t.Fatalf("%s %s on deleted space code = %v", w.method, w.path, code)
			}
		}

		// Reads never pre-flight — the collection is still on disk,
		// untouched by the refused writes.
		var q struct {
			Records []map[string]any `json:"records"`
		}
		mustJSON(t, http.MethodPost, base+"/v1/local/query", `{"coll":`+sp+`}`, http.StatusOK, &q)
		if len(q.Records) != 1 || q.Records[0]["id"] != "x" || q.Records[0]["objectId"] != "obj-1" {
			t.Fatalf("query on deleted space: %+v", q.Records)
		}
		var list api.LocalListResponse
		mustJSON(t, http.MethodGet, base+"/v1/local/collections?spaceId="+spaceId, "", http.StatusOK, &list)
		if len(list.Collections) != 1 || list.Collections[0].Count != 1 {
			t.Fatalf("space collection after space delete: %+v", list.Collections)
		}

		// Delete and drop are the cleanup path: no space pre-flight.
		var del api.LocalDeleteResponse
		mustJSON(t, http.MethodPost, base+"/v1/local/delete", `{"coll":`+sp+`,"ids":["x"]}`, http.StatusOK, &del)
		if del.Deleted != 1 {
			t.Fatalf("delete on deleted space: %+v", del)
		}
		mustStatus(t, http.MethodDelete,
			base+"/v1/local/collections?scope=space&spaceId="+spaceId+"&name=cache", "", http.StatusNoContent)
		mustJSON(t, http.MethodGet, base+"/v1/local/collections?spaceId="+spaceId, "", http.StatusOK, &list)
		if len(list.Collections) != 0 {
			t.Fatalf("space collection after drop: %+v", list.Collections)
		}
		// Account-scoped data is untouched by the space delete.
		mustJSON(t, http.MethodGet, base+"/v1/local/collections?scope=account", "", http.StatusOK, &list)
		if len(list.Collections) != 2 {
			t.Fatalf("account collections after space delete: %+v", list.Collections)
		}
	})

	srv.stop(t)
}
