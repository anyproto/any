package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerBundleDeclaredType proves a bundle-declared type
// converges across two peers that install it while apart: both ensure
// the same derived bundle with properties, and the handle-derived ids
// make them ONE definition per handle on both sides — one tree, one
// column, no losers.
func TestE2E_MultipeerBundleDeclaredType(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer bundle type test takes ~120s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"bundle-type"}`, http.StatusCreated, &sp)
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	const bundleId = "wiki-test/v1"
	body := `{"id":"` + bundleId + `","name":"Wiki","derived":true,"weight":1,"layout":{"type":"page"},` +
		`"properties":[{"xKey":"parentId","name":"Parent","kind":"string"},{"xKey":"pos","name":"Position","kind":"string"}]}`

	// Both peers install without waiting for each other: a derived
	// install is never refused, so each declares the type itself.
	var onOwner, onJoiner api.BundleEnsureResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/bundles", body, http.StatusOK, &onOwner)
	if !pollUntil(2*time.Minute, func() bool {
		onJoiner = api.BundleEnsureResponse{}
		code := tryJSON(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/bundles", body, &onJoiner)
		return code == http.StatusOK && onJoiner.Bundle.RootId != ""
	}) {
		t.Fatalf("joiner never installed the bundle: %+v", onJoiner)
	}
	if onOwner.Bundle.RootId == "" || onOwner.Bundle.RootId != onJoiner.Bundle.RootId {
		t.Fatalf("derived root diverged: owner=%q joiner=%q", onOwner.Bundle.RootId, onJoiner.Bundle.RootId)
	}
	root := onOwner.Bundle.RootId

	propIds := func(base string) map[string]string {
		var props api.PropertiesListResponse
		if tryJSON(t, http.MethodGet, base+"/v1/spaces/"+sp.Id+"/types/"+root+"/properties", "", &props) != http.StatusOK {
			return nil
		}
		out := map[string]string{}
		for _, p := range props.Properties {
			out[p.XKey] = p.Id
		}
		if len(out) != len(props.Properties) {
			return nil // a duplicated handle: two columns
		}
		return out
	}
	var a, b map[string]string
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		a, b = propIds(owner.base), propIds(joiner.base)
		return len(a) == 2 && len(b) == 2 && a["parentId"] == b["parentId"] && a["pos"] == b["pos"]
	}) {
		t.Fatalf("property ids never converged: owner=%v joiner=%v", a, b)
	}

	// One definition each, no loser, the type metadata on both sides.
	for _, p := range []*peer{owner, joiner} {
		var got api.BundleGetResponse
		mustJSON(t, http.MethodGet, p.base+"/v1/spaces/"+sp.Id+"/bundles/wiki-test%2Fv1", "", http.StatusOK, &got)
		if len(got.Bundle.Losers) != 0 || len(got.Bundle.Roots) != 1 {
			t.Errorf("%s: bundle = %+v, want one root, no losers", p.base, got.Bundle)
		}
		var info api.TypeInfo
		mustJSON(t, http.MethodGet, p.base+"/v1/spaces/"+sp.Id+"/types/"+root, "", http.StatusOK, &info)
		if info.Weight != 1 || string(info.Layout) != `{"type":"page"}` || info.Hidden {
			t.Errorf("%s: type = %+v", p.base, info)
		}
	}
}
