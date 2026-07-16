package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_GeneralChat_Derive verifies the per-space "general" chat
// object delivered through SpaceInfo: the single-space create and get
// responses carry the same deterministic id in GeneralChatObjectId
// (stable across calls), and the derived object accepts chat messages
// (its chat type was attached on first derive).
func TestServer_GeneralChat_Derive(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// Create a space; the create response must already carry the
	// general chat id (spaceToAPI derives it on first sight).
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"GeneralChatTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var created api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.GeneralChatObjectId == "" {
		t.Fatalf("create response missing generalChatObjectId: %+v", created)
	}
	spaceId := created.Id

	// GET /v1/spaces/:id carries the same id, twice (deterministic +
	// idempotent across calls).
	var g1, g2 api.SpaceInfo
	decodeGet(t, e, "/v1/spaces/"+spaceId, &g1)
	decodeGet(t, e, "/v1/spaces/"+spaceId, &g2)
	if g1.GeneralChatObjectId == "" || g1.GeneralChatObjectId != g2.GeneralChatObjectId {
		t.Fatalf("general chat id not deterministic: %q vs %q", g1.GeneralChatObjectId, g2.GeneralChatObjectId)
	}
	if g1.GeneralChatObjectId != created.GeneralChatObjectId {
		t.Fatalf("space GET id %q != create response id %q", g1.GeneralChatObjectId, created.GeneralChatObjectId)
	}

	// The derived object accepts chat writes — the chat type was
	// attached on first derive, so no explicit object create is needed.
	msgBase := "/v1/spaces/" + spaceId + "/objects/" + g1.GeneralChatObjectId
	msg := chatSend(t, e, msgBase, "hello general", "")
	if msg.Id == "" || msg.Text != "hello general" {
		t.Fatalf("send to general chat: %+v", msg)
	}
}

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
