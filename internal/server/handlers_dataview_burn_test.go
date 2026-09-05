package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/dataview"
)

// TestServer_DataView_DeletedIdIsBurned pins the sharpest edge of the
// client-supplied-id contract: deleting a view permanently burns its
// id. Re-ensuring it comes back 200 with a REJECTION and creates
// nothing, so a client that ignores `rejections` silently renders no
// views. The documented recovery is the next id in the deterministic
// sequence (docs/24-data-views.md § Ensuring the default view).
func TestServer_DataView_DeletedIdIsBurned(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)
	createView(t, e, spaceId, objectId, "default", defaultViewPayload)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/delete-records", fmt.Sprintf(`{
		"objectId": %q, "dataset": %q, "recordIds": ["default"]
	}`, objectId, dataview.DatasetViews))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete view: %d %s", rec.Code, rec.Body.String())
	}
	if got := len(listViews(t, e, spaceId, objectId)); got != 0 {
		t.Fatalf("view count after delete = %d, want 0", got)
	}

	// Re-ensuring the burned id is a 200 that did nothing.
	res := createView(t, e, spaceId, objectId, "default", defaultViewPayload)
	if len(res.Rejections) == 0 {
		t.Fatalf("re-ensuring a deleted id was accepted; the burn rule changed")
	}
	if got := len(listViews(t, e, spaceId, objectId)); got != 0 {
		t.Errorf("view count after re-ensure = %d, want 0 (a burned id creates nothing)", got)
	}

	// The next id in the sequence is the recovery, and it converges
	// because every client walks the same sequence.
	res = createView(t, e, spaceId, objectId, "default-2", defaultViewPayload)
	if len(res.Rejections) > 0 {
		t.Fatalf("fallback id rejected: %+v", res.Rejections)
	}
	views := listViews(t, e, spaceId, objectId)
	if len(views) != 1 || views[0].Id != "default-2" {
		t.Errorf("after recovery: %+v, want one view with id default-2", views)
	}
}

// TestServer_PropertiesAttachUnknownType: `any.types` is a synced DAG
// write with no validation behind it, so a typo'd type id has to be
// refused up front rather than replicated forever. Detach stays
// unchecked — it is the repair path for a row that already carries a
// bogus id.
func TestServer_PropertiesAttachUnknownType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)

	for _, typeId := range []string{"no_such_type", "undefined"} {
		rec := doJSON(t, e, http.MethodPost,
			fmt.Sprintf("/v1/spaces/%s/properties/%s/attach/%s", spaceId, objectId, typeId), "")
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
			t.Errorf("attach %q: %d %s, want a 4xx", typeId, rec.Code, rec.Body.String())
		}
	}
	for _, tp := range objectTypes(t, e, spaceId, objectId) {
		if tp == "no_such_type" || tp == "undefined" {
			t.Fatalf("a refused type reached any.types: %v", objectTypes(t, e, spaceId, objectId))
		}
	}

	// Detach removes whatever is on the row, checked or not.
	rec := doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/properties/%s/detach/%s", spaceId, objectId, "no_such_type"), "")
	if rec.Code != http.StatusOK {
		t.Errorf("detach of an unknown type: %d %s, want 200 (repair path)", rec.Code, rec.Body.String())
	}
}
