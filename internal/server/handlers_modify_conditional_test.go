package server

import (
	"context"
	"encoding/json"
	"github.com/anyproto/any-sync-sdk/space"
	"github.com/valyala/fastjson"
	"net/http"
	"strings"
	"testing"
)

func TestBuildModifyBatchConditional(t *testing.T) {
	for _, field := range []string{"ifUnchangedSince", "ifObjectUnchangedSince"} {
		t.Run(field, func(t *testing.T) {
			for _, value := range []string{"0", "42"} {
				root := fastjson.MustParse(`{"objectId":"obj","dataset":"source","records":[],"` + field + `":` + value + `}`)
				batch, err := buildModifyBatch(root)
				if err != nil {
					t.Fatal(err)
				}
				seq := batch.IfUnchangedSince
				if field == "ifObjectUnchangedSince" {
					seq = batch.IfObjectUnchangedSince
				}
				if seq == nil || (value == "42" && *seq != 42) {
					t.Fatalf("guard dropped: %+v", batch)
				}
			}
			for _, value := range []string{"null", "-1", "1.5", `"42"`, "true", "18446744073709551616"} {
				root := fastjson.MustParse(`{"objectId":"obj","dataset":"source","records":[],"` + field + `":` + value + `}`)
				if _, err := buildModifyBatch(root); err == nil {
					t.Fatalf("invalid guard accepted: %s", value)
				}
			}
		})
	}
}

func TestServer_ConditionalModifyRejectsStaleRead(t *testing.T) {
	d := newUnauthorizedDeps(t)
	d.cfg.Network, _ = placeholderNetwork(t)
	e := buildEcho(d)
	health := doJSON(t, e, http.MethodGet, "/v1/health", "")
	if !strings.Contains(health.Body.String(), "conditional-writes") {
		t.Fatal("guard support is not advertised")
	}
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	info := createSpaceInfo(t, e, "Conditional modify")
	id := mustCreateObject(t, e, info.Id, `{"type":"page"}`)
	sp, err := d.sdk.Spaces().Get(context.Background(), info.Id)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ifUnchangedSince", "ifObjectUnchangedSince"} {
		row, err := sp.Objects().Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		seq := row.GetInt("_applySeq")
		if seq <= 0 {
			t.Fatal("missing revision")
		}
		request := func(name string) string {
			b, _ := json.Marshal(map[string]any{"objectId": id, "dataset": "objects", field: seq, "records": []any{map[string]any{"id": id, "ops": []any{map[string]any{"type": "$set", "path": "any.name", "value": name}}}}})
			return string(b)
		}
		url := "/v1/spaces/" + info.Id + "/modify"
		if rec := doJSON(t, e, http.MethodPost, url, request("Winner")); rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		if rec := doJSON(t, e, http.MethodPost, url, request("Stale")); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "write.precondition_failed") {
			t.Fatalf("stale write not refused: %d %s", rec.Code, rec.Body.String())
		}
		after, err := sp.Objects().Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if string(after.GetStringBytes("any", "name")) != "Winner" {
			t.Fatal("stale write overwrote winner")
		}
	}
}

func TestServer_ConditionalInstallTombstoneRead(t *testing.T) {
	ctx := context.Background()
	d := newUnauthorizedDeps(t)
	d.cfg.Network, _ = placeholderNetwork(t)
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	info := createSpaceInfo(t, e, "Install records")
	sp, err := d.sdk.Spaces().Get(ctx, info.Id)
	if err != nil {
		t.Fatal(err)
	}
	tid, err := sp.Types().Create(ctx, space.TypeCreateParams{Name: "Program"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sp.Types().AddPart(ctx, tid, space.PartDraft{Key: "source", Datasets: []space.DatasetDraft{{Key: "source", Dynamic: true, IdRule: space.IdUser}}})
	if err != nil {
		t.Fatal(err)
	}
	oid, err := sp.Objects().Create(ctx, space.CreateObjectOpts{Type: tid})
	if err != nil {
		t.Fatal(err)
	}
	ds := tid + "_source"
	raw, _ := json.Marshal(map[string]any{"objectId": oid, "dataset": ds, "records": []any{map[string]any{"id": "main", "upsert": true, "ops": []any{map[string]any{"type": "$set", "value": map[string]any{"code": "return 1"}}}}}, "ifUnchangedSince": 0})
	if rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+info.Id+"/modify", string(raw)); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	query, _ := json.Marshal(map[string]any{"objectId": oid, "dataset": ds, "includeDeleted": true})
	read := func() map[string]any {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+info.Id+"/query", string(query))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		var data struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Records) != 1 {
			t.Fatalf("missing record: %s", rec.Body.String())
		}
		return data.Records[0]
	}
	before := read()
	if before["id"] != "main" || before["_applySeq"].(float64) <= 0 {
		t.Fatalf("wrong recovery read: %v", before)
	}
	if _, err := sp.Delete(ctx, space.DeleteBatch{ObjectId: oid, Dataset: ds, RecordIds: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	after := read()
	if after["_deletedAt"] == nil || after["_applySeq"].(float64) <= before["_applySeq"].(float64) {
		t.Fatalf("tombstone lost: %v", after)
	}
	if rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+info.Id+"/modify", string(raw)); rec.Code != 409 {
		t.Fatalf("deleted source restored: %d %s", rec.Code, rec.Body.String())
	}
}
