package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// TestServer_SpaceSync exercises POST /v1/spaces/:id/sync — the wrapper
// for Space.SyncHeads. A freshly created space has responsible nodes, so
// forcing a head-sync round should complete and return 204. The endpoint
// blocks until the round finishes; we only assert the status code, not
// any converged state (single process has nothing to converge with).
func TestServer_SpaceSync(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SyncNow"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/sync", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sync: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("sync: expected empty body, got %s", rec.Body.String())
	}
}

// The mapped SpaceInfo carries the parent id and every refusal the SDK
// names; a space with neither serialises without the fields.
func TestSpaceInfoToAPI_ParentAndRefusal(t *testing.T) {
	for refusal, want := range map[space.Refusal]string{
		space.RefusalNone:                   "",
		space.RefusalParentNotAvailable:     api.SpaceRefusalParentNotAvailable,
		space.RefusalParentInvalid:          api.SpaceRefusalParentInvalid,
		space.RefusalParentPermissionDenied: api.SpaceRefusalParentPermissionDenied,
		space.RefusalChildLimitReached:      api.SpaceRefusalChildLimitReached,
		space.RefusalHasChildren:            api.SpaceRefusalHasChildren,
	} {
		out := spaceInfoToAPI(space.SpaceInfo{Id: "child", ParentSpaceId: "parent", Refusal: refusal})
		if out.ParentSpaceId != "parent" || out.Refusal != want {
			t.Errorf("refusal %v: parentSpaceId %q refusal %q, want parent and %q", refusal, out.ParentSpaceId, out.Refusal, want)
		}
		// The wire label is the SDK's own, so the raw space-list rows and
		// the mapped shape name a refusal the same way.
		if want != "" && want != refusal.String() {
			t.Errorf("refusal %v: wire label %q differs from the row's %q", refusal, want, refusal.String())
		}
	}

	raw, err := json.Marshal(spaceInfoToAPI(space.SpaceInfo{Id: "top"}))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"parentSpaceId", "refusal"} {
		if _, ok := fields[f]; ok {
			t.Errorf("a top-level space serialises %s: %s", f, raw)
		}
	}
}
