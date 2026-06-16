package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_TypeCreate_XKeyValidation covers the server-side xKey rule
// for POST /v1/spaces/:spaceId/types: an xKey is required (it's the only
// human handle a type resolves by besides its CID) and must be unique
// within the space — colliding with an existing type's xKey OR its id.
func TestServer_TypeCreate_XKeyValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"XKeyDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	typesURL := "/v1/spaces/" + sp.Id + "/types"

	// 1. Name-only create is rejected.
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"Pages"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("name-only create: status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.xkey_required" {
		t.Errorf("name-only code = %q, want type.xkey_required", code)
	}

	// 2. With an xKey it succeeds.
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"Pages","xKey":"pages"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with xKey: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. Re-using the same xKey collides — even with a different name.
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"Other","xKey":"pages"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate xKey: status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.xkey_conflict" {
		t.Errorf("duplicate xKey code = %q, want type.xkey_conflict", code)
	}

	// 4. An xKey shadowing a built-in type's literal id also collides
	//    (built-ins carry xKey "" but resolve by id, e.g. "chat").
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"NotChat","xKey":"chat"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("builtin-id xKey: status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.xkey_conflict" {
		t.Errorf("builtin-id xKey code = %q, want type.xkey_conflict", code)
	}
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env.Error.Code
}
