package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/anyproto/any/internal/api"
)

// TestSpaceJoin_DecodeInvite_SDKContract pins the SDK contract the
// invite.invalid branch in spaceJoin relies on: a malformed token makes
// space.DecodeInvite fail with an error matching space.ErrInvalidInvite
// (via %w wrap). If the SDK ever stops wrapping the sentinel, the
// errors.Is branch in spaceJoin silently breaks and this test catches it.
// Runs without a booted SDK.
func TestSpaceJoin_DecodeInvite_SDKContract(t *testing.T) {
	_, err := space.DecodeInvite("not-a-valid-token")
	if err == nil {
		t.Fatal("DecodeInvite(bad token) = nil error, want non-nil")
	}
	if !errors.Is(err, space.ErrInvalidInvite) {
		t.Fatalf("errors.Is(err, space.ErrInvalidInvite) = false; err = %v", err)
	}
}

// TestInviteDecodeError_Envelope exercises the inviteDecodeError mapping
// directly against a synthetic echo context — no SDK boot — so the
// 400 invite.invalid contract and the static no-leak message have
// always-running coverage even when staging nodeconf is absent.
func TestInviteDecodeError_Envelope(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/spaces/join", nil), rec)

	if err := inviteDecodeError(c); err != nil {
		t.Fatalf("inviteDecodeError returned err: %v", err)
	}
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
	if env.Error.Message != "invite token is malformed or not recognized" {
		t.Errorf("message = %q, want static no-leak message", env.Error.Message)
	}
	// No-leak rule: the static message must not surface SDK internals.
	for _, leak := range []string{"spaceimpl", "anysyncsdk", "any-sync-sdk", "github.com/"} {
		if strings.Contains(env.Error.Message, leak) {
			t.Errorf("message %q leaks SDK internal %q", env.Error.Message, leak)
		}
	}
}

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
	for _, leak := range []string{"spaceimpl", "anysyncsdk", "any-sync-sdk", "github.com/"} {
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
	d.ready.Store(true) // skip the unauthorized guard; validation runs pre-SDK
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
