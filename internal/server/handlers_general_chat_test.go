package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_GeneralChat_Derive verifies the per-space "general" chat
// object: GET /v1/spaces/:id/chat resolves a deterministic id that is
// stable across calls, the single-space create/get responses carry the
// same id in GeneralChatObjectId, and the resolved object accepts chat
// messages (its chat type was attached on first derive).
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
	chatBase := "/v1/spaces/" + spaceId + "/chat"

	// The resolver returns the same id, twice (deterministic + idempotent).
	var r1, r2 api.GeneralChatResponse
	decodeGet(t, e, chatBase, &r1)
	decodeGet(t, e, chatBase, &r2)
	if r1.ObjectId == "" || r1.ObjectId != r2.ObjectId {
		t.Fatalf("general chat id not deterministic: %q vs %q", r1.ObjectId, r2.ObjectId)
	}
	if r1.ObjectId != created.GeneralChatObjectId {
		t.Fatalf("resolver id %q != create response id %q", r1.ObjectId, created.GeneralChatObjectId)
	}

	// GET /v1/spaces/:id carries the same id.
	var got api.SpaceInfo
	decodeGet(t, e, "/v1/spaces/"+spaceId, &got)
	if got.GeneralChatObjectId != r1.ObjectId {
		t.Fatalf("space GET id %q != resolver id %q", got.GeneralChatObjectId, r1.ObjectId)
	}

	// The derived object accepts chat writes — the chat type was
	// attached on first derive, so no explicit object create is needed.
	msgBase := "/v1/spaces/" + spaceId + "/objects/" + r1.ObjectId
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
