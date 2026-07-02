package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// The OneToOne / RegisterIncoming handlers validate the identity field
// before touching the SDK, so these 400 branches need no live SDK — a
// bare deps{} with ready=true (to clear the unauthorized guard) suffices
// and the tests never skip. Mirrors TestServer_SpaceJoin_MissingToken.

func TestServer_SpaceOneToOne_MissingIdentity(t *testing.T) {
	d := &deps{}
	d.ready.Store(true)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/one-to-one", `{"otherIdentity":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "request.missing_field" {
		t.Errorf("code = %q, want %q", got, "request.missing_field")
	}
}

func TestServer_SpaceRegisterIncoming_MissingIdentity(t *testing.T) {
	d := &deps{}
	d.ready.Store(true)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/one-to-one/register-incoming", `{"peerIdentity":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "request.missing_field" {
		t.Errorf("code = %q, want %q", got, "request.missing_field")
	}
}

func errEnvCode(t *testing.T, body []byte) string {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Error.Code
}
