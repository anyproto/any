package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// The bundles endpoints: clients register what they install, so these
// tests speak only the wire surface — no server-side catalog exists to
// stand in for a client.

const testBundleId = "general-chat/v1"

// escapedBundleId is the path form of a bundle id: ids carry a slash,
// which a path segment cannot hold raw.
func escapedBundleId(id string) string { return url.PathEscape(id) }

// createSpaceInfo creates a space over HTTP and returns its SpaceInfo.
func createSpaceInfo(t *testing.T, e http.Handler, name string) api.SpaceInfo {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"`+name+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var info api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return info
}

// decodeGet GETs a path expecting 200 and decodes the body.
func decodeGet(t *testing.T, e http.Handler, path string, out any) {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
}

// ensureBundle POSTs an Ensure and decodes the reply.
func ensureBundle(t *testing.T, e http.Handler, spaceId, body string) api.BundleEnsureResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/bundles", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("ensure bundle: %d %s", rec.Code, rec.Body.String())
	}
	var out api.BundleEnsureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode ensure: %v", err)
	}
	return out
}

// TestServer_BundleEnsureInstallsThenAdopts pins the core contract:
// the first call creates the root and says so, later calls adopt the
// same one and write nothing. The caller's provenance tag round-trips.
func TestServer_BundleEnsureInstallsThenAdopts(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleEnsure")
	edType := installModuleType(t, e, sp.Id, "editor")
	body := `{"id":"` + testBundleId + `","name":"General","rootType":"` + edType + `"}`

	first := ensureBundle(t, e, sp.Id, body)
	if !first.Installed {
		t.Fatalf("first ensure did not install: %+v", first)
	}
	if first.Bundle.RootId == "" {
		t.Fatalf("install produced no root: %+v", first.Bundle)
	}
	if first.Bundle.Id != testBundleId || first.Bundle.Name != "General" {
		t.Fatalf("row does not carry the request: %+v", first.Bundle)
	}
	if !slices.Contains(first.Bundle.Roots, first.Bundle.RootId) {
		t.Fatalf("winner not claimed in roots: %+v", first.Bundle)
	}
	if len(first.Bundle.Losers) != 0 {
		t.Fatalf("single-device install reported losers: %+v", first.Bundle.Losers)
	}

	second := ensureBundle(t, e, sp.Id, body)
	if second.Installed {
		t.Fatal("second ensure installed again instead of adopting")
	}
	if second.Bundle.RootId != first.Bundle.RootId {
		t.Fatalf("adopted a different root: %q vs %q", second.Bundle.RootId, first.Bundle.RootId)
	}

	// The requested type is on the root, so the install's dataset is
	// writable straight away.
	msg := mustModify(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/"+first.Bundle.RootId+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"hello bundle"}`, http.StatusCreated)
	if len(msg.RecordIds) == 0 {
		t.Fatalf("root does not accept editor writes: %+v", msg)
	}
}

// TestServer_BundleEnsureRootProperties pins that seeded root
// properties reach the object. `any.name` is not among them by design:
// the SDK stamps the bundle's own name there, which is what puts the
// root tree in the head-sync diff.
func TestServer_BundleEnsureRootProperties(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleProps")
	pageType := installModuleType(t, e, sp.Id, "editor")
	res := ensureBundle(t, e, sp.Id,
		`{"id":"notes/v1","name":"Notes","rootType":"`+pageType+`",`+
			`"rootProperties":{"any":{"description":"Seeded description"}}}`)

	var props map[string]any
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/properties/"+res.Bundle.RootId, &props)
	record, _ := props["record"].(map[string]any)
	anyProps, _ := record["any"].(map[string]any)
	if anyProps["description"] != "Seeded description" {
		t.Fatalf("root properties not seeded: %+v", props)
	}
	if anyProps["name"] != "Notes" {
		t.Fatalf("bundle name not stamped on the root: %+v", anyProps)
	}
}

// TestServer_BundleListAndGet pins the read surface, including the
// percent-encoded id in the path.
func TestServer_BundleListAndGet(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleReads")
	a := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General","rootType":"page"}`)
	b := ensureBundle(t, e, sp.Id, `{"id":"notes/v1","name":"Notes","rootType":"page"}`)

	var list api.BundleListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles", &list)
	if len(list.Bundles) != 2 {
		t.Fatalf("list = %+v, want two rows", list.Bundles)
	}
	roots := map[string]string{}
	for _, row := range list.Bundles {
		roots[row.Id] = row.RootId
	}
	if roots[testBundleId] != a.Bundle.RootId || roots["notes/v1"] != b.Bundle.RootId {
		t.Fatalf("list rows do not match the installs: %+v", list.Bundles)
	}

	var oneResp api.BundleGetResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles/"+escapedBundleId(testBundleId), &oneResp)
	one := oneResp.Bundle
	if one.Id != testBundleId || one.RootId != a.Bundle.RootId || one.Name != "General" {
		t.Fatalf("get = %+v, want the general chat row", one)
	}

	// An id nobody installed is a clean 404.
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/bundles/"+escapedBundleId("nope/v1"), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown bundle: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if env.Error.Code != api.ErrBundleNotFound {
		t.Fatalf("code = %q, want %q", env.Error.Code, api.ErrBundleNotFound)
	}
}

// TestServer_BundleChildren pins the setup-child mechanism: children
// are deterministic per (winner, seed) and re-derive to the same
// object, so a restored device reaches them from the winner alone.
func TestServer_BundleChildren(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleChildren")
	inst := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General","rootType":"page"}`)
	path := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId(testBundleId) + "/children"

	child := func(body string) api.BundleChildResponse {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, path, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("child: %d %s", rec.Code, rec.Body.String())
		}
		var out api.BundleChildResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode child: %v", err)
		}
		return out
	}

	first := child(`{"seed":"memory/v1","type":"page"}`)
	again := child(`{"seed":"memory/v1","type":"page"}`)
	if first.ObjectId == "" || first.ObjectId != again.ObjectId {
		t.Fatalf("child not deterministic: %q vs %q", first.ObjectId, again.ObjectId)
	}
	if first.ObjectId == inst.Bundle.RootId {
		t.Fatal("child collided with its root")
	}
	if other := child(`{"seed":"notes/v1","type":"page"}`); other.ObjectId == first.ObjectId {
		t.Fatalf("distinct seeds derived the same child: %q", other.ObjectId)
	}

	// A seed and a type are required, and children need an installed
	// bundle.
	if rec := doJSON(t, e, http.MethodPost, path, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing seed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodPost, path, `{"seed":"x/v1"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing type: %d %s", rec.Code, rec.Body.String())
	}
	missing := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId("nope/v1") + "/children"
	if rec := doJSON(t, e, http.MethodPost, missing, `{"seed":"x/v1","type":"page"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("child of an uninstalled bundle: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_BundleResolveRejectsNonLoser pins the resolve guard: the
// winner is not a loser, and the registry says so rather than deleting
// the live install.
func TestServer_BundleResolveRejectsNonLoser(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleResolve")
	inst := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General","rootType":"page"}`)
	path := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId(testBundleId) + "/resolve"

	rec := doJSON(t, e, http.MethodPost, path, `{"loserRootId":"`+inst.Bundle.RootId+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("resolving the winner: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if env.Error.Code != api.ErrBundleNotLoser {
		t.Fatalf("code = %q, want %q", env.Error.Code, api.ErrBundleNotLoser)
	}

	// The install survives.
	var afterResp api.BundleGetResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles/"+escapedBundleId(testBundleId), &afterResp)
	after := afterResp.Bundle
	if after.RootId != inst.Bundle.RootId {
		t.Fatalf("winner changed: %q vs %q", after.RootId, inst.Bundle.RootId)
	}

	if rec := doJSON(t, e, http.MethodPost, path, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing loserRootId: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_BundleResolveUnknownRoot pins that a root nobody claimed
// for this bundle is refused rather than deleted.
func TestServer_BundleResolveUnknownRoot(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleResolveUnknown")
	ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General","rootType":"page"}`)

	path := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId(testBundleId) + "/resolve"
	rec := doJSON(t, e, http.MethodPost, path, `{"loserRootId":"never-claimed"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("resolve of an unclaimed root: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if env.Error.Code != api.ErrBundleNotLoser {
		t.Fatalf("code = %q, want %q", env.Error.Code, api.ErrBundleNotLoser)
	}

	// Resolving against a bundle nobody installed is a 404.
	missing := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId("nope/v1") + "/resolve"
	if rec := doJSON(t, e, http.MethodPost, missing, `{"loserRootId":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("resolve on an uninstalled bundle: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_BundleEnsureValidation pins the request-shape errors.
// TestBundleError_TreeNotLocalIsNotReady: a tree this device does not
// hold yet surfaces from the SDK as ErrObjectNotFound; on the bundle
// paths that is the retryable `409 bundle.not_ready`, never the 404 the
// generic mapping would give (a joiner polls ensure right after the
// accept, and a 404 stops the poll).
func TestBundleError_TreeNotLocalIsNotReady(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
	err := bundleError(c, fmt.Errorf("open tree: %w", space.ErrObjectNotFound), "sp1", testBundleId)
	if err != nil {
		t.Fatalf("bundleError returned %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != api.ErrBundleNotReady {
		t.Fatalf("code = %q, want %q", env.Error.Code, api.ErrBundleNotReady)
	}
	if env.Error.Details["bundleId"] != testBundleId {
		t.Fatalf("details = %v, want bundleId", env.Error.Details)
	}
}

func TestServer_BundleEnsureValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleValidation")
	if rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id: %d %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/nosuchspace/bundles", `{"id":"x/v1","rootType":"page"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown space: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_BundleRegistryReadableAsDataset pins that the rows are
// also reachable through the generic dataset surface on the spaceIndex
// object — the path clients subscribe to for live conflict updates.
func TestServer_BundleRegistryReadableAsDataset(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	info := createSpaceInfo(t, e, "BundleDataset")
	inst := ensureBundle(t, e, info.Id, `{"id":"`+testBundleId+`","name":"General","rootType":"page"}`)

	sp, err := d.sdk.Spaces().Get(ctx, info.Id)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}
	var out struct {
		Records []struct {
			Id     string `json:"id"`
			Name   string `json:"name"`
			RootId string `json:"rootId"`
		} `json:"records"`
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+info.Id+"/query",
		`{"objectId":"`+sp.SpaceIndexObjectId()+`","dataset":"bundles"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query bundles: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(out.Records) != 1 || out.Records[0].Id != testBundleId ||
		out.Records[0].RootId != inst.Bundle.RootId || out.Records[0].Name != "General" {
		t.Fatalf("dataset rows = %+v", out.Records)
	}
}

// TestServer_BundleEnsurePreflight pins that a bad Ensure is rejected
// BEFORE anything is created: a type the space does not have (the
// create path would drop the attachment and report success), a
// property value violating its declared format, and an unknown body
// field. Each leaves the registry empty.
func TestServer_BundleEnsurePreflight(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundlePreflight")
	path := "/v1/spaces/" + sp.Id + "/bundles"
	userType := plainType(t, e, sp.Id)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/collections", `{"name":"Shelf","xKey":"shelf"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create collection: %d %s", rec.Code, rec.Body.String())
	}
	var created api.CollectionsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	userColl := created.CollectionId

	cases := []struct {
		name, body, code string
	}{
		{"unknown type", `{"id":"a/v1","rootType":"no_such_type"}`, "type.not_found"},
		{"rootType names a user collection", `{"id":"a/v1","rootType":"` + userColl + `"}`, "type.not_a_type"},
		{"rootCollections names a user type", `{"id":"a/v1","rootType":"page","rootCollections":["` + userType + `"]}`, "collection.not_a_collection"},
		{"rootProperties keyed by a user type that is not rootType", `{"id":"a/v1","rootType":"page","rootProperties":{"` + userType + `":{"x":1}}}`, "collection.not_a_collection"},
		{"unknown collection", `{"id":"a/v1","rootType":"page","rootCollections":["no_such_collection"]}`, "collection.not_found"},
		{"unknown rootProperties owner", `{"id":"a/v1","rootType":"page","rootProperties":{"no_such_type":{"x":1}}}`, "collection.not_found"},
		{"rootProperties keyed by a registered type that is not rootType", `{"id":"a/v1","rootType":"page","rootProperties":{"dataview":{"host":"x"}}}`, "collection.not_found"},
		{"rootType names a registered collection", `{"id":"a/v1","rootType":"miniapp"}`, "type.not_found"},
		{"rootCollections names a registered type", `{"id":"a/v1","rootType":"page","rootCollections":["dataview"]}`, "collection.not_found"},
		{"bare root without a type", `{"id":"a/v1"}`, "request.missing_field"},
		{"unknown field", `{"id":"a/v1","source":"marketplace"}`, "request.unknown_field"},
		{"rootType not a string", `{"id":"a/v1","rootType":["chat_host"]}`, "request.schema"},
		{"rootCollections not an array", `{"id":"a/v1","rootCollections":"miniapp"}`, "request.schema"},
		{"props not an object", `{"id":"a/v1","rootProperties":[]}`, "request.schema"},
		{"id too long", `{"id":"` + strings.Repeat("x", 257) + `"}`, "request.invalid_field"},
		{"name too long", `{"id":"a/v1","name":"` + strings.Repeat("x", 1025) + `"}`, "request.invalid_field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, path, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d %s, want 400", rec.Code, rec.Body.String())
			}
			var env api.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if env.Error.Code != tc.code {
				t.Fatalf("code = %q, want %q", env.Error.Code, tc.code)
			}
		})
	}

	// Nothing was installed, and no orphan root was left behind.
	var list api.BundleListResponse
	decodeGet(t, e, path, &list)
	if len(list.Bundles) != 0 {
		t.Fatalf("rejected requests registered bundles: %+v", list.Bundles)
	}
}

// TestServer_BundleChildSeedCap pins the seed bound — seeds are
// permanent, so an unbounded one would be a permanent object id.
func TestServer_BundleChildSeedCap(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleSeedCap")
	ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General","rootType":"page"}`)
	path := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId(testBundleId) + "/children"

	rec := doJSON(t, e, http.MethodPost, path, `{"seed":"`+strings.Repeat("s", 257)+`","type":"page"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "request.invalid_field") {
		t.Fatalf("oversized seed: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_BundleEnsureDerivedRoot pins the fork-proof install: the
// root is derived from the bundle id, so the row reports it as derived
// and a re-ensure adopts the very same one. Its setup objects hang off
// it by seed — any-sync refuses a derived object as a parent — and are
// still deterministic per seed.
func TestServer_BundleEnsureDerivedRoot(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleDerived")
	edType := installModuleType(t, e, sp.Id, "editor")
	body := `{"id":"` + testBundleId + `","name":"General","rootType":"` + edType + `","derived":true}`

	first := ensureBundle(t, e, sp.Id, body)
	if !first.Installed || !first.Bundle.Derived || first.Bundle.RootId == "" {
		t.Fatalf("derived install: %+v (installed=%v)", first.Bundle, first.Installed)
	}
	if !slices.Contains(first.Bundle.Roots, first.Bundle.RootId) || len(first.Bundle.Roots) != 1 {
		t.Fatalf("derived install claimed more than its own root: %+v", first.Bundle.Roots)
	}
	if len(first.Bundle.Losers) != 0 {
		t.Fatalf("derived install reported losers: %+v", first.Bundle.Losers)
	}

	second := ensureBundle(t, e, sp.Id, body)
	if second.Installed || second.Bundle.RootId != first.Bundle.RootId || !second.Bundle.Derived {
		t.Fatalf("re-ensure did not adopt the derived root: %+v (installed=%v)", second.Bundle, second.Installed)
	}

	var gotResp api.BundleGetResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles/"+escapedBundleId(testBundleId), &gotResp)
	got := gotResp.Bundle
	if !got.Derived || got.RootId != first.Bundle.RootId {
		t.Fatalf("read path lost the derived verdict: %+v", got)
	}

	// The requested type is on the root, so its dataset is writable.
	if msg := mustModify(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/"+first.Bundle.RootId+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"hello derived"}`, http.StatusCreated); len(msg.RecordIds) == 0 {
		t.Fatalf("derived root does not accept editor writes: %+v", msg)
	}

	// Children: bound by seed, deterministic, distinct per seed.
	path := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId(testBundleId) + "/children"
	child := func(body string) api.BundleChildResponse {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, path, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("child of a derived root: %d %s", rec.Code, rec.Body.String())
		}
		var out api.BundleChildResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode child: %v", err)
		}
		return out
	}
	firstChild := child(`{"seed":"memory/v1","type":"page"}`)
	if again := child(`{"seed":"memory/v1","type":"page"}`); again.ObjectId != firstChild.ObjectId {
		t.Fatalf("child not deterministic: %q vs %q", firstChild.ObjectId, again.ObjectId)
	}
	if other := child(`{"seed":"notes/v1","type":"page"}`); other.ObjectId == firstChild.ObjectId {
		t.Fatalf("distinct seeds derived the same child: %q", other.ObjectId)
	}
	if firstChild.ObjectId == first.Bundle.RootId {
		t.Fatal("child collided with its root")
	}
}

// TestServer_BundleDerivedAdoptsCreatedInstall pins that nothing
// migrates behind the client: a bundle already installed on a created
// root stays on it, and the reply says so.
func TestServer_BundleDerivedAdoptsCreatedInstall(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleDerivedAdopt")
	created := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General","rootType":"page"}`)
	if created.Bundle.Derived {
		t.Fatalf("created install reported as derived: %+v", created.Bundle)
	}

	derived := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General","derived":true,"rootType":"page"}`)
	if derived.Installed || derived.Bundle.Derived {
		t.Fatalf("derived request forked the created install: %+v (installed=%v)", derived.Bundle, derived.Installed)
	}
	if derived.Bundle.RootId != created.Bundle.RootId {
		t.Fatalf("adopted a different root: %q vs %q", derived.Bundle.RootId, created.Bundle.RootId)
	}
}

// TestServer_BundleDerivedRootProperties pins that a derived root is
// seeded like a created one — it has no create-time hook, so the
// values land as an ordinary write.
func TestServer_BundleDerivedRootProperties(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleDerivedProps")
	pageType := installModuleType(t, e, sp.Id, "editor")
	res := ensureBundle(t, e, sp.Id,
		`{"id":"notes/v1","name":"Notes","rootType":"`+pageType+`","derived":true,`+
			`"rootProperties":{"any":{"description":"Seeded description"}}}`)

	var props map[string]any
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/properties/"+res.Bundle.RootId, &props)
	record, _ := props["record"].(map[string]any)
	anyProps, _ := record["any"].(map[string]any)
	if anyProps["description"] != "Seeded description" {
		t.Fatalf("derived root properties not seeded: %+v", props)
	}
	if anyProps["name"] != "Notes" {
		t.Fatalf("bundle name not stamped on the derived root: %+v", anyProps)
	}
}

// TestServer_BundleDerivedValidation pins the flag's shape.
func TestServer_BundleDerivedValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleDerivedValidation")
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles",
		`{"id":"`+testBundleId+`","derived":"yes"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-boolean derived: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "derived must be a boolean") {
		t.Fatalf("unexpected error body: %s", rec.Body.String())
	}
}

// TestServer_BundleDerivedRootPropertyTypes pins that a derived root is
// filed under every COLLECTION its seeded properties write into, even
// when rootCollections does not name it — a value write under an owner
// the object does not have is rejected, and the created path writes the
// same membership at birth.
func TestServer_BundleDerivedRootPropertyTypes(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleDerivedPropTypes")

	docs := mustCreateCollection(t, e, sp.Id, `{"name":"Docs","xKey":"doc"}`)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/collections/"+docs+"/properties",
		`{"xKey":"topic","name":"Topic","kind":"string"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add property: %d %s", rec.Code, rec.Body.String())
	}
	var prop api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &prop); err != nil {
		t.Fatalf("decode property: %v", err)
	}

	// rootCollections names nothing; the membership comes from
	// rootProperties.
	res := ensureBundle(t, e, sp.Id,
		`{"id":"docs/v1","name":"Docs","derived":true,"rootType":"page","rootProperties":{"`+
			docs+`":{"`+prop.PropId+`":"seeded"}}}`)
	if !res.Bundle.Derived {
		t.Fatalf("not a derived install: %+v", res.Bundle)
	}

	var props map[string]any
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/properties/"+res.Bundle.RootId, &props)
	record, _ := props["record"].(map[string]any)
	colProps, _ := record[docs].(map[string]any)
	if colProps[prop.PropId] != "seeded" {
		t.Fatalf("seeded value did not land: %+v", props)
	}
	if cols := objectCollections(t, e, sp.Id, res.Bundle.RootId); !slices.Contains(cols, docs) {
		t.Fatalf("root not filed under the seeded collection: %v", cols)
	}
}

// TestServer_BundleEnsureRootPropertiesGate: a root property value that
// does not fit its descriptor slug is refused BEFORE the root is
// created, so no install and no orphan object result.
func TestServer_BundleEnsureRootPropertiesGate(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleGate")
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Task","xKey":"task"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types/"+tr.TypeId+"/properties",
		`{"name":"Due","xKey":"due","kind":"datetime","xFormat":{"type":"date"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add property: %d %s", rec.Code, rec.Body.String())
	}
	var pr api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
		t.Fatal(err)
	}

	body := `{"id":"tasks/v1","name":"Tasks","rootType":"` + tr.TypeId + `",` +
		`"rootProperties":{"` + tr.TypeId + `":{"` + pr.PropId + `":{"$date":"2026-07-03T12:00:00Z"}}}}`
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-midnight date must be refused: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "property.format_violation")
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/bundles/tasks%2Fv1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a refused install must leave nothing behind: %d %s", rec.Code, rec.Body.String())
	}

	// The same install with a fitting value goes through.
	body = `{"id":"tasks/v1","name":"Tasks","rootType":"` + tr.TypeId + `",` +
		`"rootProperties":{"` + tr.TypeId + `":{"` + pr.PropId + `":{"$date":"2026-07-03T00:00:00Z"}}}}`
	res := ensureBundle(t, e, sp.Id, body)
	if !res.Installed {
		t.Fatalf("expected a fresh install: %+v", res)
	}
}

// TestServer_BundleXKeyHealsOnAdopt pins the handle heal: an install
// that predates the handle gains it on the next ensure that declares
// one — a writer's adopt fills what the root lacks, nothing else moves.
func TestServer_BundleXKeyHealsOnAdopt(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "BundleXKeyHeal")

	parts := `"parts":[{"key":"entries","datasets":[{"key":"entries","idRule":"user","fields":[{"key":"t","kind":"string"}]}]}]`
	first := ensureBundle(t, e, sp.Id, `{"id":"heal/v1","name":"Heal",`+parts+`}`)
	if !first.Installed {
		t.Fatalf("first ensure: %+v", first)
	}
	var info api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+first.Bundle.RootId, &info)
	if info.XKey != "" {
		t.Fatalf("handle present before it was declared: %+v", info)
	}

	second := ensureBundle(t, e, sp.Id, `{"id":"heal/v1","name":"Renamed","xKey":"healed",`+parts+`}`)
	if second.Installed || second.Bundle.RootId != first.Bundle.RootId {
		t.Fatalf("second ensure did not adopt: %+v", second)
	}
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+first.Bundle.RootId, &info)
	if info.XKey != "healed" {
		t.Fatalf("handle not healed on adopt: %+v", info)
	}
	if info.Name != "Heal" {
		t.Fatalf("adopt renamed the root: %+v", info)
	}
	// A third ensure with the same handle changes nothing.
	third := ensureBundle(t, e, sp.Id, `{"id":"heal/v1","xKey":"healed",`+parts+`}`)
	if third.Installed {
		t.Fatalf("third ensure installed: %+v", third)
	}
}

// A definition implements itself: a parts-declaring root hosts its own
// bundle's records with nothing to ask for, while its row carries only
// the marker — so it never matches a query for the type it defines.
func TestServer_BundleDefinitionHostsItsRecords(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "BundleDefinitionRecords")

	parts := `"parts":[{"key":"entries","datasets":[{"key":"entries","idRule":"user","fields":[{"key":"t","kind":"string","mutableBy":"any"}]}]}]`
	upsert := func(objectId, dataset string) *httptest.ResponseRecorder {
		return doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/upsert",
			`{"objectId":"`+objectId+`","dataset":"`+dataset+`","records":[{"id":"one","fields":{"t":"x"}}]}`)
	}

	definition := ensureBundle(t, e, sp.Id, `{"id":"definition/v1","name":"Definition",`+parts+`}`)
	root := definition.Bundle.RootId
	entries := root + "_entries"
	row := objectRow(t, e, sp.Id, root)
	if rowType(row) != "__type__" || slices.Contains(rowCollections(row), root) {
		t.Fatalf("definition root membership: type=%q collections=%v", rowType(row), rowCollections(row))
	}
	if rec := upsert(root, entries); rec.Code != http.StatusOK {
		t.Fatalf("write to the definition's own records: %d %s", rec.Code, rec.Body.String())
	}
	// The definition is not a member of itself.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query", `{"filter":{"any.type":"`+root+`"}}`)
	var q api.QueryResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	if rec.Code != http.StatusOK || len(q.Records) != 0 {
		t.Fatalf("definition matched its own type: %d %s", rec.Code, rec.Body.String())
	}

	// An object that only carries the type writes there too.
	obj := mustCreateObject(t, e, sp.Id, `{"type":"`+root+`"}`)
	if rec := upsert(obj, entries); rec.Code != http.StatusOK {
		t.Fatalf("write on a typed object: %d %s", rec.Code, rec.Body.String())
	}
	// An object of another type does not.
	other := mustCreateObject(t, e, sp.Id, `{"type":"`+plainType(t, e, sp.Id)+`"}`)
	if rec := upsert(other, entries); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "dataset.not_declared") {
		t.Fatalf("write on an object of another type: %d %s", rec.Code, rec.Body.String())
	}

	// The flag is gone from the wire.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles", `{"id":"bare/v1","selfTyped":true,`+parts+`}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "request.unknown_field") {
		t.Fatalf("selfTyped on the wire: %d %s", rec.Code, rec.Body.String())
	}
}

// A collection-declaring bundle: the root takes the collection marker,
// its properties are columns objects filed under it may write, and the
// type-only extras are refused.
func TestServer_BundleDeclaresCollection(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "BundleCollection")

	out := ensureBundle(t, e, sp.Id, `{"id":"facet/v1","name":"Facet","collection":true,"xKey":"facet",`+
		`"properties":[{"xKey":"stage","name":"Stage","kind":"string"}]}`)
	if !out.Installed {
		t.Fatalf("collection bundle: %+v", out)
	}
	root := out.Bundle.RootId
	if got := rowType(objectRow(t, e, sp.Id, root)); got != "__collection__" {
		t.Fatalf("collection root any.type = %q", got)
	}
	var info api.CollectionInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections/"+root, &info)
	if info.XKey != "facet" || info.Name != "Facet" {
		t.Fatalf("collection info: %+v", info)
	}
	var defs api.PropertiesListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections/"+root+"/properties", &defs)
	if len(defs.Properties) != 1 || defs.Properties[0].XKey != "stage" {
		t.Fatalf("collection properties: %+v", defs)
	}
	// The same root on the types surface is a typed refusal.
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/"+root, "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "type.not_a_type") {
		t.Fatalf("collection on the types route: %d %s", rec.Code, rec.Body.String())
	}
	// A collection has no layout and no parts.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles",
		`{"id":"facet2/v1","collection":true,"xKey":"facet2","layout":{"type":"page"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "request.invalid_field") {
		t.Fatalf("collection with a layout: %d %s", rec.Code, rec.Body.String())
	}
}
