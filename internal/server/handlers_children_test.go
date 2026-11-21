package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/config"
)

// childSpaceErrors is every parent and child space sentinel of the SDK
// with the status and code it answers.
var childSpaceErrors = []struct {
	err      error
	wantHTTP int
	wantCode string
}{
	{space.ErrOffline, http.StatusServiceUnavailable, "network.unreachable"},
	{space.ErrParentSpaceNotAvailable, http.StatusConflict, "space.parent_not_available"},
	{space.ErrParentSpaceInvalid, http.StatusConflict, "space.parent_invalid"},
	{space.ErrParentPermissionDenied, http.StatusForbidden, "space.parent_permission_denied"},
	{space.ErrChildSpacesLimitReached, http.StatusConflict, "space.child_limit_reached"},
	{space.ErrNotParentMember, http.StatusConflict, "acl.not_parent_member"},
	{space.ErrSpaceHasChildren, http.StatusConflict, "space.has_children"},
	{space.ErrNotChildSpace, http.StatusConflict, "space.not_child"},
	{space.ErrSpaceNotStored, http.StatusConflict, "space.not_stored"},
}

// childSpaceMappers is every error mapper a child-space sentinel can
// reach, each called with the space id s1.
var childSpaceMappers = map[string]func(echo.Context, error) error{
	"spaceError": func(c echo.Context, err error) error { return spaceError(c, err, "s1") },
	"sdkOpError": func(c echo.Context, err error) error { return sdkOpError(c, err, map[string]any{"spaceId": "s1"}) },
	"aclOpError": func(c echo.Context, err error) error { return aclOpError(c, err, map[string]any{"spaceId": "s1"}) },
	"aclIdentityOpError": func(c echo.Context, err error) error {
		return aclIdentityOpError(c, err, "identities", map[string]any{"spaceId": "s1"})
	},
}

// mapError runs one error mapper and returns the status and the
// envelope it wrote.
func mapError(t *testing.T, mapper func(echo.Context) error) (int, api.ErrorEnvelope) {
	t.Helper()
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
	if err := mapper(c); err != nil {
		t.Fatalf("mapper returned %v", err)
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v: %s", err, rec.Body.String())
	}
	return rec.Code, env
}

// Every mapper answers a child-space sentinel with the same code: the
// space mapper, the generic one and the ACL ones. The ACL mappers must
// match the typed error before their "permission denied" text match.
func TestChildSpaceError_Mapping(t *testing.T) {
	for _, tc := range childSpaceErrors {
		for name, mapper := range childSpaceMappers {
			wrapped := fmt.Errorf("spaceimpl: Op: space %q: %w", "s1", tc.err)
			code, env := mapError(t, func(c echo.Context) error { return mapper(c, wrapped) })
			if code != tc.wantHTTP || env.Error.Code != tc.wantCode {
				t.Errorf("%s(%v): %d %s, want %d %s", name, tc.err, code, env.Error.Code, tc.wantHTTP, tc.wantCode)
			}
			if got := env.Error.Details["spaceId"]; got != "s1" {
				t.Errorf("%s(%v): details %v, want the spaceId kept", name, tc.err, env.Error.Details)
			}
		}
	}
}

// An identity the ACL holds no account of is the 404 of the members
// routes, on the ACL mappers.
func TestAclOpError_AccountNotFound(t *testing.T) {
	err := fmt.Errorf("spaceimpl: RemoveAccounts: %w", space.ErrAccountNotFound)
	for name, mapper := range map[string]func(echo.Context) error{
		"aclOpError": func(c echo.Context) error { return aclOpError(c, err, map[string]any{"spaceId": "s1"}) },
		"aclIdentityOpError": func(c echo.Context) error {
			return aclIdentityOpError(c, err, "identities", map[string]any{"spaceId": "s1"})
		},
	} {
		code, env := mapError(t, mapper)
		if code != http.StatusNotFound || env.Error.Code != "members.not_found" {
			t.Errorf("%s: %d %s, want 404 members.not_found", name, code, env.Error.Code)
		}
	}
}

// newOfflineServer boots a fresh account on a network whose every node
// refuses the connection: the SDK runs and reaches no coordinator and
// no node, whatever the machine's DNS answers.
func newOfflineServer(t *testing.T) (*deps, *echo.Echo) {
	t.Helper()
	d := newUnauthorizedDeps(t)
	d.cfg.Network = config.Network{Nodeconf: editNodeconf(t, config.NodeconfPlaceholder(), func(conf map[string]any) {
		nodes, _ := conf["nodes"].([]any)
		for _, n := range nodes {
			n.(map[string]any)["addresses"] = []any{"127.0.0.1:1"}
		}
	})}
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	return d, e
}

// createChild creates a child of parentId and returns its SpaceInfo.
func createChild(t *testing.T, e http.Handler, parentId, name string) api.SpaceInfo {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", fmt.Sprintf(`{"name":%q,"parentSpaceId":%q}`, name, parentId))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create child %s: %d %s", name, rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	return sp
}

// expectError asserts the status and the error code of a reply.
func expectError(t *testing.T, what string, rec *httptest.ResponseRecorder, wantHTTP int, wantCode string) {
	t.Helper()
	if rec.Code != wantHTTP || errEnvCode(t, rec.Body.Bytes()) != wantCode {
		t.Errorf("%s: %d %s, want %d %s", what, rec.Code, rec.Body.String(), wantHTTP, wantCode)
	}
}

// POST /v1/spaces with a parentSpaceId creates a child with no network,
// and refuses a parent the account's own space list rules out.
func TestServer_ChildSpace_Create(t *testing.T) {
	d, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")

	child := createChild(t, e, parent, "child")
	if child.ParentSpaceId != parent || child.Status != api.SpaceStatusActive || child.SpaceType != space.SpaceTypeAny {
		t.Fatalf("child = %+v, want an active any.space under %s", child, parent)
	}
	if child.Refusal != "" {
		t.Errorf("a child no coordinator was asked about reads refusal %q", child.Refusal)
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", fmt.Sprintf(`{"parentSpaceId":%q,"spaceType":"any.space"}`, parent))
	if rec.Code != http.StatusCreated {
		t.Errorf("a child with spaceType any.space: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/derived/bao", "")
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("derive bao: %d %s", rec.Code, rec.Body.String())
	}
	var derived api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &derived); err != nil {
		t.Fatal(err)
	}
	gone := createSpace(t, e, "gone")
	if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+gone, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete %s: %d %s", gone, rec.Code, rec.Body.String())
	}

	// A refusal for the parent names it as the request did.
	for _, tc := range []struct {
		name     string
		parentId string
		wantHTTP int
		wantCode string
	}{
		{"a child as the parent", child.Id, http.StatusConflict, "space.parent_invalid"},
		{"a derived space as the parent", derived.Id, http.StatusConflict, "space.parent_invalid"},
		{"the tech space as the parent", d.sdk.TechSpaceId(), http.StatusConflict, "space.parent_invalid"},
		{"a parent the account does not hold", unknownSpaceId(parent), http.StatusNotFound, "space.not_found"},
		{"a deleted parent", gone, http.StatusConflict, "space.deleted"},
	} {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces", fmt.Sprintf(`{"parentSpaceId":%q}`, tc.parentId))
		expectError(t, tc.name, rec, tc.wantHTTP, tc.wantCode)
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if got := env.Error.Details["parentSpaceId"]; got != tc.parentId || env.Error.Details["spaceId"] != nil {
			t.Errorf("%s: details %v, want the id as parentSpaceId alone", tc.name, env.Error.Details)
		}
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces", fmt.Sprintf(`{"parentSpaceId":%q,"spaceType":"any.onetoone"}`, parent))
	expectError(t, "another space type", rec, http.StatusBadRequest, "request.invalid_field")
}

// unknownSpaceId returns a well-formed space id no account holds: the
// content id of spaceId under another replication key.
func unknownSpaceId(spaceId string) string {
	return spaceId[:strings.LastIndexByte(spaceId, '.')] + ".1"
}

// The parent id of a child survives every way the server serialises a
// space: the create reply, the single read, the list, the raw
// space-list rows and their stream.
func TestServer_ChildSpace_ParentOnEveryRead(t *testing.T) {
	d, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	child := createChild(t, e, parent, "child")

	var got api.SpaceInfo
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+child.Id, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ParentSpaceId != parent {
		t.Errorf("GET /v1/spaces/:id: %v %s, want parentSpaceId %s", err, rec.Body.String(), parent)
	}

	var list api.SpaceListResponse
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("GET /v1/spaces: %v %s", err, rec.Body.String())
	}
	parents := map[string]string{}
	for _, sp := range list.Spaces {
		parents[sp.Id] = sp.ParentSpaceId
	}
	if p, ok := parents[child.Id]; !ok || p != parent {
		t.Errorf("GET /v1/spaces: child row parentSpaceId %q (listed %v), want %s", p, ok, parent)
	}
	if p := parents[parent]; p != "" {
		t.Errorf("GET /v1/spaces: the parent reads parentSpaceId %q", p)
	}

	// rowParent reads parentSpaceId off the raw row of the child.
	rowParent := func(what string, records []json.RawMessage) {
		t.Helper()
		for _, raw := range records {
			var row struct{ Id, ParentSpaceId string }
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatalf("%s: %v: %s", what, err, raw)
			}
			if row.Id == child.Id {
				if row.ParentSpaceId != parent {
					t.Errorf("%s: child row %s, want parentSpaceId %s", what, raw, parent)
				}
				return
			}
		}
		t.Errorf("%s: no row for the child in %s", what, records)
	}
	query := fmt.Sprintf(`{"filter":{"parentSpaceId":%q}}`, parent)
	var snap api.QueryResponse
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/query", query)
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("POST /v1/spaces/query: %v %s", err, rec.Body.String())
	}
	if len(snap.Records) != 1 {
		t.Errorf("POST /v1/spaces/query filtered on the parent: %d rows, want the one child: %s", len(snap.Records), rec.Body.String())
	}
	rowParent("POST /v1/spaces/query", snap.Records)

	// The same rows through the tech space's own query route.
	techQuery := fmt.Sprintf(`{"objectId":%q,"dataset":"spaces","filter":{"parentSpaceId":%q}}`,
		d.sdk.Spaces().SpaceIndexObjectId(), parent)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+d.sdk.TechSpaceId()+"/query", techQuery)
	snap = api.QueryResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("tech space query: %v %s", err, rec.Body.String())
	}
	rowParent("tech space query", snap.Records)

	// The refusal is a readable row field: a filter on it is taken, and
	// no space of this account was refused.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/query", `{"filter":{"refusal":{"$in":["parent_not_available","parent_invalid","parent_permission_denied","child_limit_reached","has_children"]}}}`)
	snap = api.QueryResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil || rec.Code != http.StatusOK || len(snap.Records) != 0 {
		t.Errorf("POST /v1/spaces/query filtered on refusal: %d %v %s, want 200 and no rows", rec.Code, err, rec.Body.String())
	}

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	frames, done := openStream(ctx, func(ctx context.Context, _ string, body []byte, fn func(client.SSEFrame) error) error {
		return cl.StreamSpaceListQuerySubscribe(ctx, body, fn)
	}, "", query)
	rowParent("POST /v1/spaces/query/subscribe snapshot", readSnapshot(t, frames).Records)

	// A child created while the stream is open arrives with its parent.
	second := createChild(t, e, parent, "second")
	evs := collectChanges(t, frames, func(evs []api.QuerySubscribeEvent) bool {
		for _, ev := range evs {
			if len(ev.Added) > 0 {
				return true
			}
		}
		return false
	})
	var added bool
	for _, ev := range evs {
		for _, a := range ev.Added {
			var row struct{ Id, ParentSpaceId string }
			if err := json.Unmarshal(a.Doc, &row); err != nil {
				t.Fatalf("decode added row: %v: %s", err, a.Doc)
			}
			if row.Id == second.Id {
				added = true
				if row.ParentSpaceId != parent {
					t.Errorf("stream: added row %s, want parentSpaceId %s", a.Doc, parent)
				}
			}
		}
	}
	if !added {
		t.Errorf("stream: the new child was not added: %+v", evs)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the stream did not end with its context")
	}
}

// A space with a child in the account's own list is not deleted, with
// no network; a child is deleted like any space, and its parent after
// it.
func TestServer_ChildSpace_DeleteParentAfterChildren(t *testing.T) {
	_, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	child := createChild(t, e, parent, "child")

	rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+parent, "")
	expectError(t, "delete a parent with a child", rec, http.StatusConflict, "space.has_children")
	var got api.SpaceInfo
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+parent, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Status != api.SpaceStatusActive {
		t.Fatalf("the refused parent: %v %s, want it active", err, rec.Body.String())
	}

	if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+child.Id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete the child: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+parent, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete the parent after its child: %d %s", rec.Code, rec.Body.String())
	}
}
