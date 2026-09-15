package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// The one-to-one key-exchange dataset never reaches a client: every
// per-object read route refuses it by name, on a regular space, before
// any SDK call — so the refusal holds whether or not the space is a
// 1-1, whether or not the object or version exists.
func TestServer_IdentityKeysReadRefused(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"KeysFence"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	idx := sp.SpaceIndexObjectId
	if idx == "" {
		t.Fatal("space info carries no spaceIndexObjectId")
	}
	base := "/v1/spaces/" + sp.Id

	refused := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: decode error envelope: %v", name, err)
		}
		if env.Error.Code != "request.invalid_field" {
			t.Fatalf("%s: error code = %q, want request.invalid_field", name, env.Error.Code)
		}
	}
	body := `{"objectId":"` + idx + `","dataset":"identityKeys"}`
	refused("query", doJSON(t, e, http.MethodPost, base+"/query", body))
	refused("aggregate", doJSON(t, e, http.MethodPost, base+"/aggregate",
		`{"objectId":"`+idx+`","dataset":"identityKeys","pipeline":[{"$count":"n"}]}`))
	hist := base + "/objects/" + idx + "/history"
	refused("history view", doJSON(t, e, http.MethodGet, hist+"/v1?dataset=identityKeys", ""))
	refused("history record", doJSON(t, e, http.MethodGet, hist+"/v1/datasets/identityKeys/records/x", ""))
	refused("history diff", doJSON(t, e, http.MethodGet, hist+"/diff?version=v1&dataset=identityKeys", ""))

	// Any other dataset on the same object still goes through to the
	// SDK (the bundles registry is readable by design).
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+idx+`","dataset":"bundles"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundles query: code = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
