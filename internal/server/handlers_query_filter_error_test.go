package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// decodeErrEnvelope pulls the canonical error envelope off a response.
func decodeErrEnvelope(t *testing.T, body []byte) api.APIError {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env.Error
}

// TestUnknownFilterOperator pins SYN-78: an operator outside the filter
// grammar is a caller fault and must come back as a typed 400 naming the bad
// token — not as 500 internal, which reads as "the server broke" and sends
// the caller off reporting a server fault instead of fixing the filter.
//
// Covers both query scopes, since each builds its query through a different
// path (buildSharedQuery vs buildPerObjectQuery).
func TestUnknownFilterOperator(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"FilterErrs"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &sp)

	cases := []struct {
		name string
		path string
		body string
	}{
		{
			name: "objects query",
			path: "/v1/spaces/" + sp.Id + "/objects/query",
			body: `{"filter":{"any.type":{"$contains":"chat"}}}`,
		},
		{
			name: "per-object dataset query",
			path: "/v1/spaces/" + sp.Id + "/query",
			body: `{"objectId":"someobj","dataset":"chat_messages",` +
				`"filter":{"text":{"$contains":"hi"}}}`,
		},
		{
			name: "nested under $and",
			path: "/v1/spaces/" + sp.Id + "/objects/query",
			body: `{"filter":{"$and":[{"any.type":{"$contains":"chat"}}]}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, tc.path, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			apiErr := decodeErrEnvelope(t, rec.Body.Bytes())
			if apiErr.Code != "filter.unknown_operator" {
				t.Errorf("code=%q, want filter.unknown_operator", apiErr.Code)
			}
			// The message must name the offending token, list the grammar,
			// and point at the array rule — the whole point is that the
			// caller can fix the filter from the response alone.
			if !strings.Contains(apiErr.Message, "$contains") {
				t.Errorf("message does not name the bad operator: %q", apiErr.Message)
			}
			if !strings.Contains(apiErr.Message, "$in") {
				t.Errorf("message does not list the grammar: %q", apiErr.Message)
			}
			if !strings.Contains(apiErr.Message, "array elements") {
				t.Errorf("message does not explain the array rule: %q", apiErr.Message)
			}
			if got := apiErr.Details["operator"]; got != "$contains" {
				t.Errorf("details.operator=%v, want $contains", got)
			}
		})
	}
}

// TestFilterInvalid pins the non-vocabulary grammar violations: since
// any-store#152 every parse rejection is a structured
// *query.ParseError, mapped to 400 filter.invalid with the parser's
// path locating the failure — parsed at the request boundary, before
// any space or tree work.
func TestFilterInvalid(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"FilterInvalid"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &sp)

	cases := []struct {
		name, body, wantInMsg string
	}{
		{"$and not an array", `{"filter":{"$and":{"a":1}}}`, "$and must be an array"},
		{"bad $regex", `{"filter":{"name":{"$regex":"["}}}`, "name.$regex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			apiErr := decodeErrEnvelope(t, rec.Body.Bytes())
			if apiErr.Code != "filter.invalid" {
				t.Errorf("code=%q, want filter.invalid (body=%s)", apiErr.Code, rec.Body.String())
			}
			if !strings.Contains(apiErr.Message, tc.wantInMsg) {
				t.Errorf("message %q does not contain %q", apiErr.Message, tc.wantInMsg)
			}
		})
	}
}

// TestScalarFilterMatchesArrayElement verifies the advice the
// filter.unknown_operator message gives. The message tells the caller that
// $contains is absent because a scalar already compares against array
// elements — if that were not true end-to-end, the error would be sending
// people down a wrong path. So assert it through the real stack rather than
// trusting a reading of any-store's Comp.Ok.
func TestScalarFilterMatchesArrayElement(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"ArrMatch"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &sp)

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Note","xKey":"note"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)

	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/types/%s/properties", sp.Id, tr.TypeId),
		`{"name":"Tags","kind":"array","xKey":"tags"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add prop: %d %s", rec.Code, rec.Body.String())
	}
	var pr api.AddPropertyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pr)

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		fmt.Sprintf(`{"type":%q}`, tr.TypeId))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var o api.ObjectsCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &o)

	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/properties/%s/set/%s", sp.Id, o.ObjectId, tr.TypeId),
		fmt.Sprintf(`{"patch":{%q:["food","travel"]}}`, pr.PropId))
	if rec.Code != http.StatusOK {
		t.Fatalf("set tags: %d %s", rec.Code, rec.Body.String())
	}

	// The scalar spelling the error message recommends.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		fmt.Sprintf(`{"filter":{"%s.%s":"food"}}`, tr.TypeId, pr.PropId))
	if rec.Code != http.StatusOK {
		t.Fatalf("scalar-vs-array query: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(resp.Records) != 1 {
		t.Fatalf("scalar filter matched %d records, want 1 — a scalar must "+
			"match inside an array, which is what filter.unknown_operator "+
			"tells callers to rely on; body=%s", len(resp.Records), rec.Body.String())
	}
}

// TestSubscribeLimitRequiresSort pins the window precondition: a live
// window has to be ordered, and the SDK's check lives in an internal
// package, so an unguarded `limit` without `sort` reached sdkOpError
// unclassified and answered 500 — a client body mistake reading as a
// server fault. Covers both parse paths (buildBodyQuery and
// buildPerObjectQuery) and confirms snapshots are unaffected, where an
// unordered limit is just an arbitrary page.
func TestSubscribeLimitRequiresSort(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SubWindow"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &sp)
	base := "/v1/spaces/" + sp.Id

	obj := mustCreateModuleObject(t, e, sp.Id, "editor")

	refused := []struct{ path, body string }{
		{base + "/objects/query/subscribe", `{"limit":1}`},
		{base + "/query/subscribe", `{"objectId":"` + obj + `","dataset":"editor_blocks","limit":1}`},
		{"/v1/spaces/query/subscribe", `{"limit":1}`},
		{"/v1/devices/query/subscribe", `{"limit":1}`},
	}
	for _, tc := range refused {
		rec := doJSON(t, e, http.MethodPost, tc.path, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400: %s", tc.path, rec.Code, rec.Body.String())
		}
		if code := decodeErrEnvelope(t, rec.Body.Bytes()).Code; code != "request.invalid_field" {
			t.Errorf("%s: code = %s, want request.invalid_field", tc.path, code)
		}
	}

	// Snapshots keep taking an unordered limit.
	for _, path := range []string{base + "/objects/query", "/v1/spaces/query"} {
		if rec := doJSON(t, e, http.MethodPost, path, `{"limit":1}`); rec.Code != http.StatusOK {
			t.Errorf("%s snapshot: status = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
	}
}
