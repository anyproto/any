package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_MembersInvites_Owner exercises the owner-side surface
// against a fresh single-account space:
//
//   - GET /v1/spaces/:id/members → owner only.
//   - GET /v1/spaces/:id/members/me → same.
//   - GET /v1/spaces/:id/members/requests → empty.
//   - GET /v1/spaces/:id/invites → empty initially.
//   - POST /v1/spaces/:id/invites → mints a token + record.
//   - GET /v1/spaces/:id/invites → reflects the new record.
//   - DELETE /v1/spaces/:id/invites/:recordId → revokes.
//   - GET /v1/spaces/:id/invites → empty again.
//
// CreateInvite calls SpaceMakeShareable, which contacts the
// coordinator. With staging this is the only network round-trip in
// the test — the rest are local.
func TestServer_MembersInvites_Owner(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"sharing-test"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	// GET /members → exactly one (the owner).
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/members", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list members: %d %s", rec.Code, rec.Body.String())
	}
	var ml api.MembersListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ml); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(ml.Members) != 1 {
		t.Fatalf("members = %d, want 1: %+v", len(ml.Members), ml.Members)
	}
	if ml.Members[0].Permission != api.SpacePermissionOwner {
		t.Errorf("owner permission = %q, want owner", ml.Members[0].Permission)
	}
	if ml.Members[0].Status != api.MemberStatusActive {
		t.Errorf("owner status = %q, want active", ml.Members[0].Status)
	}

	// GET /members/me → same.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/members/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("members/me: %d %s", rec.Code, rec.Body.String())
	}
	var me api.Member
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Identity != ml.Members[0].Identity {
		t.Errorf("me.Identity = %q, want %q", me.Identity, ml.Members[0].Identity)
	}

	// GET /members/requests → empty.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/members/requests", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("requests: %d %s", rec.Code, rec.Body.String())
	}
	var jr api.JoinRequestsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &jr); err != nil {
		t.Fatalf("decode requests: %v", err)
	}
	if len(jr.Requests) != 0 {
		t.Errorf("requests = %d, want 0", len(jr.Requests))
	}

	// GET /invites → empty initially.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/invites", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list invites empty: %d %s", rec.Code, rec.Body.String())
	}
	var inv api.InvitesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatalf("decode invites: %v", err)
	}
	if len(inv.Invites) != 0 {
		t.Fatalf("invites = %+v, want []", inv.Invites)
	}

	// POST /invites → mint.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/invites", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint invite: %d %s", rec.Code, rec.Body.String())
	}
	var minted api.InviteCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &minted); err != nil {
		t.Fatalf("decode mint: %v", err)
	}
	if minted.SpaceId != sp.Id {
		t.Errorf("minted.spaceId = %q, want %q", minted.SpaceId, sp.Id)
	}
	if len(minted.InviteToken) < 16 {
		t.Errorf("invite token looks too short: %q", minted.InviteToken)
	}

	// GET /invites → contains one record. Capture the record id for
	// the revoke call.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/invites", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list invites: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatalf("decode invites: %v", err)
	}
	if len(inv.Invites) != 1 || inv.Invites[0].RecordId == "" {
		t.Fatalf("invites = %+v, want one with recordId", inv.Invites)
	}
	recordId := inv.Invites[0].RecordId

	// DELETE /invites/:recordId → 204.
	rec = doJSON(t, e, http.MethodDelete,
		"/v1/spaces/"+sp.Id+"/invites/"+recordId, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke invite: %d %s", rec.Code, rec.Body.String())
	}

	// GET /invites → empty again.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/invites", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatalf("decode invites: %v", err)
	}
	if len(inv.Invites) != 0 {
		t.Errorf("post-revoke invites = %+v, want []", inv.Invites)
	}
}

// TestServer_MembersInvites_BadInputs covers the API-layer guards —
// no SDK round-trip needed, so this test runs without staging.
func TestServer_MembersInvites_BadInputs(t *testing.T) {
	d := &deps{}
	d.ready.Store(true) // skip the unauthorized guard; validation runs pre-SDK
	e := buildEcho(d)

	cases := []struct {
		name, method, path, body, wantCode string
		wantStatus                          int
	}{
		{
			name:       "join missing token",
			method:     http.MethodPost,
			path:       "/v1/spaces/join",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "request.missing_field",
		},
		{
			name:       "join bad json",
			method:     http.MethodPost,
			path:       "/v1/spaces/join",
			body:       `{not json`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "request.bad_json",
		},
		{
			name:       "accept missing record",
			method:     http.MethodPost,
			path:       "/v1/spaces/spc/acl/accept",
			body:       `{"permission":"writer"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "request.missing_field",
		},
		{
			name:       "accept bad permission",
			method:     http.MethodPost,
			path:       "/v1/spaces/spc/acl/accept",
			body:       `{"requestRecordId":"rec","permission":"god"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "request.schema",
		},
		{
			name:       "permissions empty",
			method:     http.MethodPost,
			path:       "/v1/spaces/spc/acl/permissions",
			body:       `{"changes":[]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "request.missing_field",
		},
		{
			name:       "remove empty",
			method:     http.MethodPost,
			path:       "/v1/spaces/spc/acl/remove",
			body:       `{"identities":[]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "request.missing_field",
		},
		{
			name:       "ownership missing newOwner",
			method:     http.MethodPost,
			path:       "/v1/spaces/spc/acl/ownership",
			body:       `{"oldOwnerPerm":"admin"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "request.missing_field",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, tc.method, tc.path, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var env api.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if !strings.HasPrefix(env.Error.Code, tc.wantCode) {
				t.Errorf("code = %q, want prefix %q", env.Error.Code, tc.wantCode)
			}
		})
	}
}
