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
// sequence (docs/24-data-views.md § Ensuring the defaults).
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
