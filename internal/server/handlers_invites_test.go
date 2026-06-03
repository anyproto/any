package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_SpaceJoin_InvalidToken posts a malformed invite token to
// POST /v1/spaces/join and asserts the SDK's decode failure is mapped to
// 400 invite.invalid (not 500 internal). The bad token fails inside
// DecodeInvite immediately, with no network round-trip, so the test is
// fast and deterministic. Needs a real SDK (the call reaches Spaces().Join)
// so it boots newTestDeps and skips when staging nodeconf is absent.
func TestServer_SpaceJoin_InvalidToken(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/join",
		`{"inviteToken":"not-a-valid-token"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "invite.invalid" {
		t.Errorf("code = %q, want %q", env.Error.Code, "invite.invalid")
	}
	// No-leak rule: the message must not surface SDK internals.
	for _, leak := range []string{"spaceimpl", "anysyncsdk", "any-sync-sdk", "/"} {
		if strings.Contains(env.Error.Message, leak) {
			t.Errorf("message %q leaks SDK internal %q", env.Error.Message, leak)
		}
	}
}

// TestServer_SpaceJoin_MissingToken guards that an empty inviteToken still
// short-circuits to 400 request.missing_field before any SDK call — the
// new invite.invalid branch must not swallow this earlier validation. No
// SDK is needed: validation runs before Spaces().Join, so a bare deps{}
// suffices and the test never skips.
func TestServer_SpaceJoin_MissingToken(t *testing.T) {
	d := &deps{}
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/join", `{"inviteToken":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "request.missing_field" {
		t.Errorf("code = %q, want %q", env.Error.Code, "request.missing_field")
	}
}
