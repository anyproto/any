package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anyproto/any-sync/util/crypto"

	"github.com/anyproto/any/internal/api"
)

// TestServer_SpaceGet_PendingNotMaterialized pins the spaceGet contract
// for non-active rows: a pending incoming 1-1 is served straight from
// the tech-space index — 200 with the row info, no spaceIndexObjectId
// — and nothing is materialized. Materializing
// would SpacePull the space ciphertext before the user accepted it.
func TestServer_SpaceGet_PendingNotMaterialized(t *testing.T) {
	d, cleanup := newTestDeps(t)
	defer cleanup()
	e := buildEcho(d)

	// A fabricated peer identity: RegisterIncoming is pure derivation +
	// a tech-space row, no network contact with the peer.
	_, pub, err := crypto.GenerateRandomEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	peer := pub.Account()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/one-to-one/register-incoming",
		`{"peerIdentity":"`+peer+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("register-incoming: status %d body=%s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces?status=one_to_one_pending", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list pending: status %d body=%s", rec.Code, rec.Body.String())
	}
	var list api.SpaceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Spaces) != 1 {
		t.Fatalf("pending rows = %d, want 1; body=%s", len(list.Spaces), rec.Body.String())
	}
	id := list.Spaces[0].Id

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get pending: status %d body=%s", rec.Code, rec.Body.String())
	}
	var info api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info.Status != api.SpaceStatusOneToOnePending {
		t.Errorf("status = %q, want %q", info.Status, api.SpaceStatusOneToOnePending)
	}
	if info.SpaceIndexObjectId != "" {
		t.Errorf("pending row must not carry materialized ids; got spaceIndex=%q",
			info.SpaceIndexObjectId)
	}

	// The GET must not have created any-sync storage for the space.
	var found []string
	_ = filepath.WalkDir(d.root, func(path string, _ fs.DirEntry, err error) error {
		if err == nil && strings.Contains(filepath.Base(path), id) {
			found = append(found, path)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("pending space left storage on disk: %v", found)
	}
}
