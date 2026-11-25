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

// POST /v1/spaces creates a space of the type spaceType names: any.space,
// also with none, or an organization. Any other type is the request's
// fault, the one of a child included: that takes a parentSpaceId.
func TestServer_SpaceCreate_SpaceType(t *testing.T) {
	_, e := newOfflineServer(t)

	for body, want := range map[string]string{
		`{"name":"plain"}`:                              space.SpaceTypeAny,
		`{"name":"named","spaceType":"any.space"}`:      space.SpaceTypeAny,
		`{"name":"org","spaceType":"any.organization"}`: space.SpaceTypeOrganization,
	} {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces", body)
		var sp api.SpaceInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %v %s", body, rec.Code, err, rec.Body.String())
		}
		if sp.Type != want || sp.SpaceType != want || sp.ParentSpaceId != "" || sp.Status != api.SpaceStatusActive {
			t.Errorf("%s: %+v, want an active top-level space of type %s", body, sp, want)
		}
	}

	for _, spaceType := range []string{space.SpaceTypeOrganizationSpace, space.SpaceTypeOneToOne, "any.techspace", "anytype.space"} {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"spaceType":"`+spaceType+`"}`)
		expectError(t, "spaceType "+spaceType, rec, http.StatusBadRequest, "request.invalid_field")
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if got := env.Error.Details["spaceType"]; got != spaceType {
			t.Errorf("spaceType %s: details %v, want the type named", spaceType, env.Error.Details)
		}
	}
}

// A filter of the space list on type, the header type of the row,
// matches that type alone: any.space lists neither an organization nor
// its child. A filter that names the three types lists all three.
func TestServer_SpaceList_FilterOnType(t *testing.T) {
	_, e := newOfflineServer(t)
	plain := createSpace(t, e, "plain")
	org := createOrganization(t, e, "org")
	child := createChild(t, e, org, "child").Id

	type row struct{ Id, Type string }
	query := func(filter string) []row {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/query", `{"filter":{"type":`+filter+`}}`)
		var snap struct{ Records []row }
		if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("query on %s: %d %v %s", filter, rec.Code, err, rec.Body.String())
		}
		return snap.Records
	}

	byType := map[string]string{
		space.SpaceTypeAny:               plain,
		space.SpaceTypeOrganization:      org,
		space.SpaceTypeOrganizationSpace: child,
	}
	for typ, want := range byType {
		if rows := query(`"` + typ + `"`); len(rows) != 1 || rows[0] != (row{want, typ}) {
			t.Errorf("query on %s: %+v, want the one row of %s", typ, rows, want)
		}
	}

	rows := query(`{"$in":["any.space","any.organization","any.organization.space"]}`)
	if len(rows) != len(byType) {
		t.Fatalf("query on the three types: %+v, want %d rows", rows, len(byType))
	}
	for _, r := range rows {
		if byType[r.Type] != r.Id {
			t.Errorf("query on the three types: row %+v, want the space of that type", r)
		}
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
