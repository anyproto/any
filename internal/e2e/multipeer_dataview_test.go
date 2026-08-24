// Multipeer saved-views e2e (SYN-175): the shared tier really is
// shared — the owner's view records reach a joiner, the joiner edits
// one and the owner sees the edit — while the device tier really is
// device-local: `localSettings` written on one peer never appears on
// the other.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/dataview"
)

// viewOn reads one data_views record from base, or ok=false if absent.
func viewOn(t *testing.T, base, spaceId, objectId, viewId string) (map[string]any, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId,
		"dataset":  dataview.Dataset,
		"filter":   map[string]any{"id": viewId},
	})
	resp, raw := doRequest(t, http.MethodPost, base+"/v1/spaces/"+spaceId+"/query", string(body))
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var qr api.QueryResponse
	if err := json.Unmarshal(raw, &qr); err != nil || len(qr.Records) == 0 {
		return nil, false
	}
	var rec map[string]any
	if err := json.Unmarshal(qr.Records[0], &rec); err != nil {
		t.Fatalf("decode view record: %v (%s)", err, qr.Records[0])
	}
	return rec, true
}

// writeView posts one modify batch against the data_views dataset.
func writeView(t *testing.T, base, spaceId, objectId, scope, ops string) api.ModifyResult {
	t.Helper()
	// Local scope addresses existing records only — no upsert, explicit
	// ids (docs/03-api.md § Modify records).
	scopeField, upsert := "", `"upsert": true, `
	if scope != "" {
		scopeField = fmt.Sprintf(`"scope": %q,`, scope)
	}
	if scope == "local" {
		upsert = ""
	}
	body := fmt.Sprintf(`{
		"objectId": %q, "dataset": %q, %s
		"records": [{"id": "default", %s"ops": %s}]
	}`, objectId, dataview.Dataset, scopeField, upsert, ops)
	var res api.ModifyResult
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceId+"/modify", body, http.StatusOK, &res)
	if len(res.Rejections) > 0 {
		t.Fatalf("modify rejected: %+v", res.Rejections)
	}
	return res
}

func TestE2E_MultipeerDataViews(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"views"}`, http.StatusCreated, &sp)

	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	var attach api.ModifyResult
	mustJSON(t, http.MethodPost,
		fmt.Sprintf("%s/v1/spaces/%s/properties/%s/attach/%s", owner.base, sp.Id, obj.ObjectId, dataview.TypeId),
		"", http.StatusOK, &attach)

	writeView(t, owner.base, sp.Id, obj.ObjectId, "", `[{"type": "$set", "path": "", "value": {
		"name": "All", "layout": "table", "pos": "a0",
		"query": {"type": "plain", "filter": {"any.types": "page"}, "sort": ["-modifiedAt"]}
	}}]`)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// The shared tier reaches the joiner, opaque query blob intact.
	peers := []*peer{owner, joiner}
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, peers, func() bool {
		rec, ok := viewOn(t, joiner.base, sp.Id, obj.ObjectId, "default")
		if !ok || rec["name"] != "All" {
			return false
		}
		q, _ := rec["query"].(map[string]any)
		return q["type"] == "plain"
	}) {
		rec, _ := viewOn(t, joiner.base, sp.Id, obj.ObjectId, "default")
		t.Fatalf("joiner never saw the owner's view: %+v", rec)
	}

	// Any writer retunes a shared view — the joiner's edit converges
	// back to the owner.
	writeView(t, joiner.base, sp.Id, obj.ObjectId, "", `[{"type": "$set", "path": "name", "value": "Urgent"}]`)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, peers, func() bool {
		rec, ok := viewOn(t, owner.base, sp.Id, obj.ObjectId, "default")
		return ok && rec["name"] == "Urgent"
	}) {
		rec, _ := viewOn(t, owner.base, sp.Id, obj.ObjectId, "default")
		t.Fatalf("owner never saw the joiner's rename: %+v", rec)
	}

	// The device tier stays put: a local-scope write mints no DAG
	// change and is invisible on the other peer.
	res := writeView(t, owner.base, sp.Id, obj.ObjectId, "local",
		`[{"type": "$set", "path": "localSettings", "value": {"widths": {"name": 480}}}]`)
	if res.ChangeId != "" {
		t.Errorf("local write minted a DAG changeId %q", res.ChangeId)
	}

	ownerRec, ok := viewOn(t, owner.base, sp.Id, obj.ObjectId, "default")
	if !ok {
		t.Fatal("owner lost its own view after the local write")
	}
	if _, present := ownerRec["localSettings"]; !present {
		t.Errorf("owner's local write not readable back: %+v", ownerRec)
	}

	// Give the write every chance to leak, then assert it did not.
	forceSync(t, sp.Id, peers...)
	time.Sleep(5 * time.Second)
	forceSync(t, sp.Id, peers...)
	joinerRec, ok := viewOn(t, joiner.base, sp.Id, obj.ObjectId, "default")
	if !ok {
		t.Fatal("joiner lost the view")
	}
	if _, leaked := joinerRec["localSettings"]; leaked {
		t.Errorf("localSettings synced to the joiner: %+v", joinerRec["localSettings"])
	}
}
