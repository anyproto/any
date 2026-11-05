package server

import (
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_BundleUninstall pins the uninstall route: the root is
// deleted, the id reads as not installed, a repeat is 204, and a fresh
// ensure mints a new root that lists no loser. A bundle never
// registered is 404; a derived install is refused and stays.
func TestServer_BundleUninstall(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleUninstall")
	edType := installModuleType(t, e, sp.Id, "editor")
	body := `{"id":"` + testBundleId + `","name":"General","rootType":"` + edType + `"}`
	first := ensureBundle(t, e, sp.Id, body)
	path := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId(testBundleId)

	if rec := doJSON(t, e, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("uninstall: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodGet, path, ""); rec.Code != http.StatusNotFound || errEnvCode(t, rec.Body.Bytes()) != api.ErrBundleNotFound {
		t.Fatalf("uninstalled bundle still reads: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/objects/"+first.Bundle.RootId, ""); rec.Code != http.StatusGone {
		t.Fatalf("root object after uninstall: %d %s, want 410", rec.Code, rec.Body.String())
	}
	// The dead row still answers 204: idempotent.
	if rec := doJSON(t, e, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat uninstall: %d %s", rec.Code, rec.Body.String())
	}

	second := ensureBundle(t, e, sp.Id, body)
	if !second.Installed || second.Bundle.RootId == first.Bundle.RootId {
		t.Fatalf("reinstall did not mint a fresh root: %+v (installed=%v)", second.Bundle, second.Installed)
	}
	if len(second.Bundle.Losers) != 0 || len(second.Bundle.Roots) != 2 {
		t.Fatalf("fresh install inherits the old root: %+v", second.Bundle)
	}

	// Never registered: 404.
	rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+sp.Id+"/bundles/"+escapedBundleId("nope/v1"), "")
	if rec.Code != http.StatusNotFound || errEnvCode(t, rec.Body.Bytes()) != api.ErrBundleNotFound {
		t.Fatalf("unknown bundle: %d %s", rec.Code, rec.Body.String())
	}

	// A derived install is permanent: refused, nothing deleted.
	derived := ensureBundle(t, e, sp.Id, `{"id":"fixed/v1","name":"Fixed","rootType":"`+edType+`","derived":true}`)
	derivedPath := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId("fixed/v1")
	rec = doJSON(t, e, http.MethodDelete, derivedPath, "")
	if rec.Code != http.StatusConflict || errEnvCode(t, rec.Body.Bytes()) != "object.derived_undeletable" {
		t.Fatalf("derived uninstall: %d %s", rec.Code, rec.Body.String())
	}
	var still api.BundleGetResponse
	decodeGet(t, e, derivedPath, &still)
	if still.Bundle.RootId != derived.Bundle.RootId {
		t.Fatalf("derived install changed after a refused uninstall: %+v", still.Bundle)
	}
}
