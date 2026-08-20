package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

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
	body := `{"id":"` + testBundleId + `","name":"General","rootTypes":["chat"]}`

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
	msg := chatSend(t, e, "/v1/spaces/"+sp.Id+"/objects/"+first.Bundle.RootId, "hello bundle", "")
	if msg.Id == "" {
		t.Fatalf("root does not accept chat writes: %+v", msg)
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
	res := ensureBundle(t, e, sp.Id,
		`{"id":"notes/v1","name":"Notes","rootTypes":["page"],`+
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
	a := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General"}`)
	b := ensureBundle(t, e, sp.Id, `{"id":"notes/v1","name":"Notes"}`)

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

	var one api.Bundle
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles/"+escapedBundleId(testBundleId), &one)
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
	inst := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General"}`)
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

	first := child(`{"seed":"memory/v1","types":["agent_memory"]}`)
	again := child(`{"seed":"memory/v1","types":["agent_memory"]}`)
	if first.ObjectId == "" || first.ObjectId != again.ObjectId {
		t.Fatalf("child not deterministic: %q vs %q", first.ObjectId, again.ObjectId)
	}
	if first.ObjectId == inst.Bundle.RootId {
		t.Fatal("child collided with its root")
	}
	if other := child(`{"seed":"notes/v1"}`); other.ObjectId == first.ObjectId {
		t.Fatalf("distinct seeds derived the same child: %q", other.ObjectId)
	}

	// A seed is required, and children need an installed bundle.
	if rec := doJSON(t, e, http.MethodPost, path, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing seed: %d %s", rec.Code, rec.Body.String())
	}
	missing := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId("nope/v1") + "/children"
	if rec := doJSON(t, e, http.MethodPost, missing, `{"seed":"x/v1"}`); rec.Code != http.StatusNotFound {
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
	inst := ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General"}`)
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
	var after api.Bundle
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles/"+escapedBundleId(testBundleId), &after)
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
	ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General"}`)

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
func TestServer_BundleEnsureValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleValidation")
	if rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id: %d %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/nosuchspace/bundles", `{"id":"x/v1"}`)
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
	inst := ensureBundle(t, e, info.Id, `{"id":"`+testBundleId+`","name":"General"}`)

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

	cases := []struct {
		name, body, code string
	}{
		{"unknown type", `{"id":"a/v1","rootTypes":["no_such_type"]}`, "type.not_found"},
		{"unknown property type", `{"id":"a/v1","rootProperties":{"no_such_type":{"x":1}}}`, "type.not_found"},
		{"unknown field", `{"id":"a/v1","source":"marketplace"}`, "request.unknown_field"},
		{"types not an array", `{"id":"a/v1","rootTypes":"chat"}`, "request.schema"},
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
	ensureBundle(t, e, sp.Id, `{"id":"`+testBundleId+`","name":"General"}`)
	path := "/v1/spaces/" + sp.Id + "/bundles/" + escapedBundleId(testBundleId) + "/children"

	rec := doJSON(t, e, http.MethodPost, path, `{"seed":"`+strings.Repeat("s", 257)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized seed: %d %s", rec.Code, rec.Body.String())
	}
}
