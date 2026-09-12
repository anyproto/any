package e2e

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerCatalog proves the usecase catalog across two
// peers: the owner sets `crm` up — its whole dependency closure — and
// the joiner's own setup ADOPTS every root, with the same property ids
// per handle, off nothing but the registry rows that sync with the
// space. A person the joiner creates under the adopted type is readable
// on the owner under the same column.
func TestE2E_MultipeerCatalog(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer catalog test takes ~120s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"catalog"}`, http.StatusCreated, &sp)

	setupBody := `{"spaceId":"` + sp.Id + `"}`
	var ownerSetup api.CatalogSetupResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/catalog/crm/setup", setupBody, http.StatusOK, &ownerSetup)
	want := []string{"system:person/v1", "system:organization/v1", "system:contact/v1", "system:contacts/v1", "system:deal/v1", "system:crm/v1"}
	if len(ownerSetup.Bundles) != len(want) {
		t.Fatalf("owner setup: %+v", ownerSetup)
	}
	byId := map[string]api.CatalogSetupBundle{}
	for i, b := range ownerSetup.Bundles {
		if b.Id != want[i] || !b.Installed {
			t.Fatalf("owner setup order/installed: %+v", ownerSetup.Bundles)
		}
		byId[b.Id] = b
	}
	person := byId["system:person/v1"]
	if person.TypeId == "" || person.Properties["email"] == "" {
		t.Fatalf("person type unresolved: %+v", person)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// The joiner runs the same setup and must adopt everything: until
	// the registry and the roots have synced it is refused with the
	// retryable 409, never handed an id it cannot write.
	var joinerSetup api.CatalogSetupResponse
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		joinerSetup = api.CatalogSetupResponse{}
		code := tryJSON(t, http.MethodPost, joiner.base+"/v1/catalog/crm/setup", setupBody, &joinerSetup)
		return code == http.StatusOK && len(joinerSetup.Bundles) == len(want)
	}) {
		t.Fatalf("joiner never adopted the catalog install: %+v", joinerSetup)
	}
	for _, b := range joinerSetup.Bundles {
		o := byId[b.Id]
		if b.Installed {
			t.Fatalf("joiner installed a competing root for %s: %+v", b.Id, b)
		}
		if b.Bundle.RootId != o.Bundle.RootId || b.TypeId != o.TypeId {
			t.Fatalf("%s diverged: owner=%q joiner=%q", b.Id, o.Bundle.RootId, b.Bundle.RootId)
		}
		for xk, id := range o.Properties {
			if b.Properties[xk] != id {
				t.Fatalf("%s property %s: owner=%q joiner=%q", b.Id, xk, id, b.Properties[xk])
			}
		}
		if o.Miniapp != nil && b.Miniapp["bundle"] != o.Miniapp["bundle"] {
			t.Fatalf("%s miniapp: owner=%v joiner=%v", b.Id, o.Miniapp, b.Miniapp)
		}
	}

	// The joiner writes a person under the adopted column; the owner
	// reads it under the same one.
	var created api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{"types":["`+person.TypeId+`"],"initialProperties":{"`+person.TypeId+`":{"`+person.Properties["email"]+`":"joiner@example.com"}}}`,
		http.StatusCreated, &created)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		// 404 until the object's tree lands on the owner — a plain
		// request, not tryJSON, which fails on any non-retryable status.
		resp, raw := doRequest(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/objects/"+created.ObjectId, "")
		return resp.StatusCode == http.StatusOK && strings.Contains(string(raw), "joiner@example.com")
	}) {
		t.Fatalf("the joiner's person did not reach the owner")
	}

	// A sidebar app that brings a type converges the same way: the
	// journal type and its `date` column come from the install, so an
	// entry one peer writes is a journal entry on the other — which is
	// what a client-minted type per device could never guarantee.
	var ownerJournal api.CatalogSetupResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/catalog/journal/setup", setupBody, http.StatusOK, &ownerJournal)
	oj := ownerJournal.Bundles[0]
	if oj.TypeId == "" || oj.Properties["date"] == "" {
		t.Fatalf("owner journal setup: %+v", oj)
	}
	var joinerJournal api.CatalogSetupResponse
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		joinerJournal = api.CatalogSetupResponse{}
		code := tryJSON(t, http.MethodPost, joiner.base+"/v1/catalog/journal/setup", setupBody, &joinerJournal)
		return code == http.StatusOK && len(joinerJournal.Bundles) == 1 && !joinerJournal.Bundles[0].Installed
	}) {
		t.Fatalf("joiner never adopted the journal install: %+v", joinerJournal)
	}
	jj := joinerJournal.Bundles[0]
	if jj.TypeId != oj.TypeId || jj.Properties["date"] != oj.Properties["date"] {
		t.Fatalf("journal type diverged: owner=%+v joiner=%+v", oj, jj)
	}
	day := `{"$date":"2026-09-12T00:00:00.000Z"}`
	var entry api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{"types":["`+jj.TypeId+`"],"initialProperties":{"`+jj.TypeId+`":{"`+jj.Properties["date"]+`":`+day+`}}}`,
		http.StatusCreated, &entry)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		resp, raw := doRequest(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects/query",
			`{"filter":{"any.types":"`+oj.TypeId+`","`+oj.TypeId+`.`+oj.Properties["date"]+`":`+day+`}}`)
		return resp.StatusCode == http.StatusOK && strings.Contains(string(raw), entry.ObjectId)
	}) {
		t.Fatalf("the joiner's journal entry did not reach the owner's day query")
	}
}
