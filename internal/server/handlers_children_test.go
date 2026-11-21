package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/anyproto/any-sync/coordinator/coordinatorproto"
	"github.com/anyproto/any-sync/util/crypto"

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
	"childOpError": func(c echo.Context, err error) error { return childOpError(c, err, "s1", "identities") },
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
// space mapper (create, delete, retry-push, children), the generic one,
// the ACL ones (members, invites, join) and the one of the parent-side
// routes. The ACL mappers must match the typed error before their
// "permission denied" text match.
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

// A parentSpaceId that is not a space id is the request's fault on
// create, 400 with the field named, for exactly the ids the children
// list refuses the same way; a well-formed id the account does not hold
// is 404.
func TestServer_ChildSpace_Create_MalformedParent(t *testing.T) {
	_, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	contentId := parent[:strings.LastIndexByte(parent, '.')]

	for id, malformed := range map[string]bool{
		"not-a-space-id":        true,
		"bafyunknown.space":     true,
		contentId:               true,
		contentId + ".":         true,
		".1apx":                 true,
		contentId + ".not base": true,
		unknownSpaceId(parent):  false,
		parent:                  false,
	} {
		create := doJSON(t, e, http.MethodPost, "/v1/spaces", fmt.Sprintf(`{"parentSpaceId":%q}`, id))
		children := doJSON(t, e, http.MethodGet, "/v1/spaces/"+url.PathEscape(id)+"/children", "")
		if !malformed {
			if create.Code == http.StatusBadRequest || children.Code == http.StatusBadRequest {
				t.Errorf("%q: create %d, children %d, want neither refused as malformed", id, create.Code, children.Code)
			}
			continue
		}
		expectError(t, fmt.Sprintf("children of %q", id), children, http.StatusBadRequest, "request.invalid_field")
		expectError(t, fmt.Sprintf("create under %q", id), create, http.StatusBadRequest, "request.invalid_field")
		var env api.ErrorEnvelope
		if err := json.Unmarshal(create.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if got := env.Error.Details["field"]; got != "parentSpaceId" {
			t.Errorf("create under %q: details %v, want field parentSpaceId", id, env.Error.Details)
		}
	}
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

// POST …/retry-push answers from the account's own list before any
// coordinator is asked: an unknown, a deleted and a top-level space, the
// tech space, and a child of the account's own parent no node holds.
func TestServer_ChildSpace_RetryPush(t *testing.T) {
	d, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	child := createChild(t, e, parent, "child")
	gone := createChild(t, e, parent, "gone")
	if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+gone.Id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete %s: %d %s", gone.Id, rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		name     string
		spaceId  string
		wantHTTP int
		wantCode string
	}{
		{"a space the account does not hold", unknownSpaceId(parent), http.StatusNotFound, "space.not_found"},
		{"not a space id", "bafyunknown.space", http.StatusBadRequest, "request.invalid_field"},
		{"a top-level space", parent, http.StatusConflict, "space.not_child"},
		{"a deleted child", gone.Id, http.StatusConflict, "space.deleted"},
		{"the tech space", d.sdk.TechSpaceId(), http.StatusMethodNotAllowed, "space.unsupported"},
		{"a child of an own parent no node holds", child.Id, http.StatusConflict, "space.parent_not_available"},
	} {
		expectError(t, tc.name, doJSON(t, e, http.MethodPost, "/v1/spaces/"+tc.spaceId+"/retry-push", ""), tc.wantHTTP, tc.wantCode)
	}

	// The coordinator was not asked, so nothing is recorded as refused.
	var got api.SpaceInfo
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+child.Id, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Refusal != "" {
		t.Errorf("the child after a retry with no coordinator: %v %s, want no refusal", err, rec.Body.String())
	}
}

// GET …/children is the coordinator's answer and nothing else: with no
// coordinator in reach it is 503, also for the account's own parent.
func TestServer_ChildSpace_Children(t *testing.T) {
	d, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	createChild(t, e, parent, "child")

	for _, tc := range []struct {
		name     string
		spaceId  string
		wantHTTP int
		wantCode string
	}{
		{"no coordinator in reach", parent, http.StatusServiceUnavailable, "network.unreachable"},
		{"not a space id", "not-a-space-id", http.StatusBadRequest, "request.invalid_field"},
		{"the tech space", d.sdk.TechSpaceId(), http.StatusMethodNotAllowed, "space.unsupported"},
	} {
		expectError(t, tc.name, doJSON(t, e, http.MethodGet, "/v1/spaces/"+tc.spaceId+"/children", ""), tc.wantHTTP, tc.wantCode)
	}
}

// The coordinator's states of a child keep their wire labels.
func TestChildStatusString(t *testing.T) {
	for status, want := range map[space.ChildStatus]string{
		space.ChildStatusUnknown:         api.ChildStatusUnknown,
		space.ChildStatusActive:          api.ChildStatusActive,
		space.ChildStatusDeletionPending: api.ChildStatusDeletionPending,
		space.ChildStatusDeleted:         api.ChildStatusDeleted,
	} {
		if got := childStatusString(status); got != want || got != status.String() {
			t.Errorf("status %d: %q, want %q, the SDK's label %q", status, got, want, status.String())
		}
	}
}

// The parent-side routes answer what the child's ACL and the request
// itself refuse, beside the child-space sentinels.
func TestChildOpError_Mapping(t *testing.T) {
	for _, tc := range []struct {
		err       error
		wantHTTP  int
		wantCode  string
		wantField string
	}{
		{space.ErrInsufficientPermissions, http.StatusForbidden, "acl.forbidden", ""},
		{space.ErrAccountNotFound, http.StatusNotFound, "members.not_found", ""},
		{space.ErrSpaceUnknown, http.StatusNotFound, "space.not_found", ""},
		{space.ErrSpaceDeleted, http.StatusConflict, "space.deleted", ""},
		{space.ErrBadSpaceId, http.StatusBadRequest, "request.invalid_field", "childId"},
		{space.ErrBadIdentity, http.StatusBadRequest, "request.invalid_field", "newOwner"},
	} {
		wrapped := fmt.Errorf("spaceimpl: ChangeChildOwner: %w", tc.err)
		code, env := mapError(t, func(c echo.Context) error { return childOpError(c, wrapped, "s1", "newOwner") })
		if code != tc.wantHTTP || env.Error.Code != tc.wantCode {
			t.Errorf("childOpError(%v): %d %s, want %d %s", tc.err, code, env.Error.Code, tc.wantHTTP, tc.wantCode)
		}
		if got, _ := env.Error.Details["field"].(string); got != tc.wantField {
			t.Errorf("childOpError(%v): details %v, want field %q", tc.err, env.Error.Details, tc.wantField)
		}
	}
}

// A space the coordinator does not know, or holds as deleted or in
// deletion, answers on the children list and on the parent-side routes
// the codes of a space the account's own list lacks or holds deleted.
// The SDK wraps the coordinator's own error in its sentinel.
func TestChildSpace_CoordinatorUnknownOrDeleted(t *testing.T) {
	mappers := map[string]func(echo.Context, error) error{
		"children list": func(c echo.Context, err error) error { return spaceError(c, err, "s1") },
		"parent side":   func(c echo.Context, err error) error { return childOpError(c, err, "s1", "identities") },
	}
	for _, tc := range []struct {
		sentinel, cause error
		wantHTTP        int
		wantCode        string
	}{
		{space.ErrSpaceUnknown, coordinatorproto.ErrSpaceNotExists, http.StatusNotFound, "space.not_found"},
		{space.ErrSpaceDeleted, coordinatorproto.ErrSpaceIsDeleted, http.StatusConflict, "space.deleted"},
		{space.ErrSpaceDeleted, coordinatorproto.ErrSpaceDeletionPending, http.StatusConflict, "space.deleted"},
	} {
		for name, mapper := range mappers {
			err := fmt.Errorf("spaceimpl: Op: %w: %w", tc.sentinel, tc.cause)
			code, env := mapError(t, func(c echo.Context) error { return mapper(c, err) })
			if code != tc.wantHTTP || env.Error.Code != tc.wantCode {
				t.Errorf("%s (%v): %d %s, want %d %s", name, tc.cause, code, env.Error.Code, tc.wantHTTP, tc.wantCode)
			}
			if got := env.Error.Details["spaceId"]; got != "s1" {
				t.Errorf("%s (%v): details %v, want the spaceId", name, tc.cause, env.Error.Details)
			}
		}
	}
}

// The parent-side members read carries identity, role and status and no
// profile, and always says whether a key rotation is pending.
func TestChildMembersToAPI(t *testing.T) {
	members := []space.ChildMember{
		{Identity: "A1", Permission: space.PermissionOwner, Status: space.MemberStatusActive},
		{Identity: "A2", Permission: space.PermissionNone, Status: space.MemberStatusRemoved},
	}
	for pending, want := range map[bool]string{
		true: `{"members":[{"identity":"A1","permission":"owner","status":"active"},` +
			`{"identity":"A2","permission":"none","status":"removed"}],"rotationPending":true}`,
		false: `{"members":[{"identity":"A1","permission":"owner","status":"active"},` +
			`{"identity":"A2","permission":"none","status":"removed"}],"rotationPending":false}`,
	} {
		body, err := json.Marshal(childMembersToAPI(members, pending))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != want {
			t.Errorf("pending %v: %s, want %s", pending, body, want)
		}
	}
	if body, _ := json.Marshal(childMembersToAPI(nil, false)); string(body) != `{"members":[],"rotationPending":false}` {
		t.Errorf("no members: %s", body)
	}
}

// newIdentity returns the account identity of a fresh key.
func newIdentity(t *testing.T) string {
	t.Helper()
	_, pub, err := crypto.GenerateRandomEd25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return pub.Account()
}

// The parent-side routes take the child's id alone. Each refuses what
// the request and the account's own list rule out, and with no network
// in reach answers 503 and changes nothing.
func TestServer_ChildSpace_ParentSide(t *testing.T) {
	d, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	child := createChild(t, e, parent, "child")
	member := newIdentity(t)
	remove := fmt.Sprintf(`{"identities":[%q]}`, member)
	handOver := fmt.Sprintf(`{"newOwner":%q,"oldOwnerPerm":"writer"}`, member)

	routes := []struct {
		name, method, suffix, body string
	}{
		{"members", http.MethodGet, "/members", ""},
		{"remove", http.MethodPost, "/acl/remove", remove},
		{"ownership", http.MethodPost, "/acl/ownership", handOver},
		{"delete", http.MethodDelete, "", ""},
	}
	for _, r := range routes {
		for _, tc := range []struct {
			name     string
			childId  string
			wantHTTP int
			wantCode string
		}{
			{"no network in reach", child.Id, http.StatusServiceUnavailable, "network.unreachable"},
			{"a top-level space", parent, http.StatusConflict, "space.not_child"},
			{"the tech space", d.sdk.TechSpaceId(), http.StatusConflict, "space.not_child"},
			{"not a space id", "not-a-space-id", http.StatusBadRequest, "request.invalid_field"},
		} {
			rec := doJSON(t, e, r.method, "/v1/children/"+tc.childId+r.suffix, r.body)
			expectError(t, r.name+": "+tc.name, rec, tc.wantHTTP, tc.wantCode)
		}
	}

	for _, tc := range []struct {
		name, suffix, body string
		wantHTTP           int
		wantCode           string
	}{
		{"remove nobody", "/acl/remove", `{"identities":[]}`, http.StatusBadRequest, "request.missing_field"},
		{"remove a malformed identity", "/acl/remove", `{"identities":["nope"]}`, http.StatusBadRequest, "request.invalid_field"},
		{"remove with an unknown key", "/acl/remove", `{"identity":"x"}`, http.StatusBadRequest, "request.unknown_field"},
		{"ownership without a new owner", "/acl/ownership", `{"oldOwnerPerm":"writer"}`, http.StatusBadRequest, "request.missing_field"},
		{"ownership to a malformed identity", "/acl/ownership", `{"newOwner":"nope","oldOwnerPerm":"writer"}`, http.StatusBadRequest, "request.invalid_field"},
	} {
		rec := doJSON(t, e, http.MethodPost, "/v1/children/"+child.Id+tc.suffix, tc.body)
		expectError(t, tc.name, rec, tc.wantHTTP, tc.wantCode)
	}

	// Nothing above reached the network, so the child is as it was.
	var got api.SpaceInfo
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+child.Id, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Status != api.SpaceStatusActive {
		t.Errorf("the child after the refused calls: %v %s, want it active", err, rec.Body.String())
	}
}

// The role the previous owner keeps is reader, writer or admin, on the
// ownership route of a space and on the parent-side one of a child. The
// server refuses any other value with a 400 before the ACL is asked.
func TestServer_Ownership_OldOwnerPerm(t *testing.T) {
	_, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	child := createChild(t, e, parent, "child")
	member := newIdentity(t)

	for route, path := range map[string]string{
		"a space":              "/v1/spaces/" + parent + "/acl/ownership",
		"a child, parent side": "/v1/children/" + child.Id + "/acl/ownership",
	} {
		for _, tc := range []struct {
			name, oldOwnerPerm string
			wantCode           string
		}{
			{"omitted", "", "request.missing_field"},
			{"none", `"none"`, "request.invalid_field"},
			{"guest", `"guest"`, "request.invalid_field"},
			{"owner", `"owner"`, "request.invalid_field"},
			{"unknown", `"boss"`, "request.schema"},
		} {
			body := fmt.Sprintf(`{"newOwner":%q}`, member)
			if tc.oldOwnerPerm != "" {
				body = fmt.Sprintf(`{"newOwner":%q,"oldOwnerPerm":%s}`, member, tc.oldOwnerPerm)
			}
			rec := doJSON(t, e, http.MethodPost, path, body)
			expectError(t, route+": oldOwnerPerm "+tc.name, rec, http.StatusBadRequest, tc.wantCode)
		}
		// The three roles pass the check: what answers next is the ACL
		// or the network, never the request validation.
		for _, role := range []string{"reader", "writer", "admin"} {
			rec := doJSON(t, e, http.MethodPost, path, fmt.Sprintf(`{"newOwner":%q,"oldOwnerPerm":%q}`, member, role))
			if rec.Code == http.StatusBadRequest {
				t.Errorf("%s: oldOwnerPerm %s: %d %s, want it past the request validation", route, role, rec.Code, rec.Body.String())
			}
		}
	}
}

// A removal that names an identity twice is the request's fault on both
// removal routes: 400 with the list named, before the ACL is asked.
func TestServer_ACLRemove_DuplicateIdentities(t *testing.T) {
	_, e := newOfflineServer(t)
	parent := createSpace(t, e, "parent")
	child := createChild(t, e, parent, "child")
	twice := newIdentity(t)
	body := fmt.Sprintf(`{"identities":[%q,%q,%q]}`, twice, newIdentity(t), twice)

	for route, path := range map[string]string{
		"a space":              "/v1/spaces/" + parent + "/acl/remove",
		"a child, parent side": "/v1/children/" + child.Id + "/acl/remove",
	} {
		rec := doJSON(t, e, http.MethodPost, path, body)
		expectError(t, route, rec, http.StatusBadRequest, "request.invalid_field")
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if got := env.Error.Details["field"]; got != "identities" {
			t.Errorf("%s: details %v, want field identities", route, env.Error.Details)
		}
	}
}
