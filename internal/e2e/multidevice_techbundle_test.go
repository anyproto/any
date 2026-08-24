// Two `any` servers sharing ONE account (same mnemonic, distinct device
// keys) install an account-level bundle on the tech space: device A
// ensures notes/v1 with an `entries` dataset and writes records,
// device B restores, reads them through the tech-space routes, writes
// its own and A converges.
package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

const scratchDatasetsBody = `[{
	"name": "entries",
	"idRule": "user",
	"idPattern": "^(any://o/.+|f:[A-Za-z0-9_-]{1,64})$",
	"idMaxLen": 256,
	"fields": [
		{"key": "title", "kind": "string", "required": true},
		{"key": "parentId", "kind": "string", "mutableBy": "any"},
		{"key": "creator", "stamp": "creator"},
		{"key": "createdAt", "stamp": "createTime"}
	]
}]`

func countRecords(t *testing.T, base, spaceId, objectId, dataset string) int {
	t.Helper()
	resp, raw := doRequest(t, http.MethodPost, base+"/v1/spaces/"+spaceId+"/query",
		`{"objectId":"`+objectId+`","dataset":"`+dataset+`"}`)
	if resp.StatusCode != http.StatusOK {
		return -1
	}
	var q api.QueryResponse
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	return len(q.Records)
}

func TestE2E_MultideviceTechBundle(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multidevice test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	devA := startPeer(t, bin, "devA")
	defer devA.stop(t)

	var accA api.AccountResponse
	mustJSON(t, http.MethodGet, devA.base+"/v1/account", "", http.StatusOK, &accA)
	if accA.TechSpaceId == "" {
		t.Fatal("account has no techSpaceId")
	}
	tech := accA.TechSpaceId
	techBase := func(base string) string { return base + "/v1/spaces/" + tech }

	var ens api.BundleEnsureResponse
	mustJSON(t, http.MethodPost, techBase(devA.base)+"/bundles",
		`{"id":"notes/v1","name":"Notes","derived":true,"datasets":`+scratchDatasetsBody+`}`,
		http.StatusOK, &ens)
	if !ens.Installed || ens.Bundle.RootId == "" {
		t.Fatalf("devA ensure: %+v", ens)
	}
	root := ens.Bundle.RootId

	var up api.UpsertResult
	mustJSON(t, http.MethodPost, techBase(devA.base)+"/upsert",
		`{"objectId":"`+root+`","dataset":"entries","records":[
			{"id":"any://o/one","fields":{"title":"One"}},
			{"id":"f:folder","fields":{"title":"Folder"}}]}`, http.StatusOK, &up)
	if up.Created != 2 {
		t.Fatalf("devA upsert: %+v", up)
	}

	devB := startPeerWithMnemonic(t, bin, "devB", walletMnemonic(t, devA.dataDir))
	defer devB.stop(t)

	var accB api.AccountResponse
	mustJSON(t, http.MethodGet, devB.base+"/v1/account", "", http.StatusOK, &accB)
	if accB.TechSpaceId != tech {
		t.Fatalf("tech space id differs: A=%s B=%s", tech, accB.TechSpaceId)
	}

	// Restore: the bundle row and the root's records reach B.
	start := time.Now()
	if !pollUntil(3*time.Minute, func() bool {
		resp, raw := doRequest(t, http.MethodGet, techBase(devB.base)+"/bundles/notes%2Fv1", "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var b api.BundleGetResponse
		if err := json.Unmarshal(raw, &b); err != nil || b.Bundle.RootId != root {
			return false
		}
		return countRecords(t, devB.base, tech, root, "entries") == 2
	}) {
		t.Fatalf("devB never restored the tech bundle (root %s)", root)
	}
	t.Logf("devB restored the tech bundle in %v", time.Since(start).Round(time.Millisecond))

	// Adopt on B declares nothing new; the datasets route lists the
	// declaration under typeId = rootId.
	var adopted api.BundleEnsureResponse
	mustJSON(t, http.MethodPost, techBase(devB.base)+"/bundles",
		`{"id":"notes/v1","derived":true,"datasets":`+scratchDatasetsBody+`}`, http.StatusOK, &adopted)
	if adopted.Installed || adopted.Bundle.RootId != root {
		t.Fatalf("devB ensure must adopt: %+v", adopted)
	}
	var defs api.TypeDatasetsListResponse
	mustJSON(t, http.MethodGet, techBase(devB.base)+"/types/"+root+"/datasets", "", http.StatusOK, &defs)
	if len(defs.Datasets) != 1 || defs.Datasets[0].Name != "entries" {
		t.Fatalf("devB datasets: %+v", defs)
	}

	// B writes, A converges.
	mustJSON(t, http.MethodPost, techBase(devB.base)+"/upsert",
		`{"objectId":"`+root+`","dataset":"entries","records":[
			{"id":"any://o/two","fields":{"title":"Two","parentId":"f:folder"}}]}`, http.StatusOK, &up)
	if up.Created != 1 {
		t.Fatalf("devB upsert: %+v", up)
	}
	start = time.Now()
	if !pollUntil(2*time.Minute, func() bool {
		return countRecords(t, devA.base, tech, root, "entries") == 3
	}) {
		t.Fatal("devA never saw devB's entry")
	}
	t.Logf("devB → devA entry converged in %v", time.Since(start).Round(time.Millisecond))
}
