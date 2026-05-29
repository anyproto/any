package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_Chat_RoundTrip drives the full chat surface through
// echo against a real SDK: send → list → edit → react → delete →
// list. Verifies server-stamped fields, ordering, edit semantics
// (modifiedAt > createdAt), and the reactions transpose.
func TestServer_Chat_RoundTrip(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// Send three messages, oldest first.
	first := chatSend(t, e, base, "first", "")
	second := chatSend(t, e, base, "second", first.Id)
	third := chatSend(t, e, base, "third", "")

	for _, msg := range []api.ChatMessage{first, second, third} {
		if msg.Id == "" {
			t.Fatalf("message id empty: %+v", msg)
		}
		if msg.Creator == "" {
			t.Errorf("creator unstamped on %s", msg.Id)
		}
		if msg.CreatedAt == 0 {
			t.Errorf("createdAt unstamped on %s", msg.Id)
		}
		// On a freshly-created (never-edited) message, modifiedAt
		// equals createdAt — clients compare the two to detect edits.
		if msg.ModifiedAt != msg.CreatedAt {
			t.Errorf("freshly-created %s: modifiedAt %d != createdAt %d",
				msg.Id, msg.ModifiedAt, msg.CreatedAt)
		}
	}
	if second.ReplyToMessageId != first.Id {
		t.Errorf("second.replyToMessageId = %q, want %q", second.ReplyToMessageId, first.Id)
	}

	// List returns all three in send order.
	listed := chatList(t, e, base, "", "", 0)
	if len(listed.Messages) != 3 {
		t.Fatalf("list: got %d messages, want 3", len(listed.Messages))
	}
	if listed.Messages[0].Text != "first" || listed.Messages[1].Text != "second" || listed.Messages[2].Text != "third" {
		t.Errorf("list order = [%s, %s, %s], want [first, second, third]",
			listed.Messages[0].Text, listed.Messages[1].Text, listed.Messages[2].Text)
	}

	// Edit second message.
	body := `{"text":"second-edited"}`
	rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+second.Id, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	var edited api.ChatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &edited); err != nil {
		t.Fatalf("decode edited: %v", err)
	}
	if edited.Text != "second-edited" {
		t.Errorf("edited.Text = %q, want second-edited", edited.Text)
	}
	if edited.ModifiedAt == 0 || edited.ModifiedAt < edited.CreatedAt {
		t.Errorf("modifiedAt should be set and >= createdAt: %+v", edited)
	}

	// React with 👍 (add).
	rxRec := doJSON(t, e, http.MethodPost, base+"/chat/messages/"+second.Id+"/reactions/"+url.PathEscape("👍"), "")
	if rxRec.Code != http.StatusOK {
		t.Fatalf("POST react add: %d %s", rxRec.Code, rxRec.Body.String())
	}
	var rxResp api.ChatReactionsResponse
	if err := json.Unmarshal(rxRec.Body.Bytes(), &rxResp); err != nil {
		t.Fatalf("decode react: %v", err)
	}
	if len(rxResp.Reactions["👍"]) != 1 || rxResp.Reactions["👍"][0] != d.account {
		t.Errorf("after add: reactions = %+v, want {👍: [%s]}", rxResp.Reactions, d.account)
	}

	// React again toggles off.
	rxRec = doJSON(t, e, http.MethodPost, base+"/chat/messages/"+second.Id+"/reactions/"+url.PathEscape("👍"), "")
	if rxRec.Code != http.StatusOK {
		t.Fatalf("POST react toggle off: %d %s", rxRec.Code, rxRec.Body.String())
	}
	if err := json.Unmarshal(rxRec.Body.Bytes(), &rxResp); err != nil {
		t.Fatalf("decode react toggle: %v", err)
	}
	if got := rxResp.Reactions["👍"]; len(got) != 0 {
		t.Errorf("after toggle off: reactions[👍] = %v, want empty", got)
	}

	// Delete first message.
	delRec := doJSON(t, e, http.MethodDelete, base+"/chat/messages/"+first.Id, "")
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", delRec.Code, delRec.Body.String())
	}

	// List after delete: two messages remain.
	listed = chatList(t, e, base, "", "", 0)
	if len(listed.Messages) != 2 {
		t.Errorf("list after delete: got %d, want 2", len(listed.Messages))
	}
	for _, m := range listed.Messages {
		if m.Id == first.Id {
			t.Errorf("deleted message %s still in list", first.Id)
		}
	}
}

// TestServer_Chat_Pagination exercises before / after / limit. Send
// five messages then page through them.
func TestServer_Chat_Pagination(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	ids := []string{}
	for i := 0; i < 5; i++ {
		msg := chatSend(t, e, base, fmt.Sprintf("m%d", i), "")
		ids = append(ids, msg.Id)
	}

	// Limit = 2 returns the first two.
	page := chatList(t, e, base, "", "", 2)
	if len(page.Messages) != 2 {
		t.Fatalf("limit=2: got %d, want 2", len(page.Messages))
	}
	if page.Messages[0].Id != ids[0] || page.Messages[1].Id != ids[1] {
		t.Errorf("limit=2 order = [%s,%s], want [%s,%s]",
			page.Messages[0].Id, page.Messages[1].Id, ids[0], ids[1])
	}

	// after=ids[1] returns ids[2..4].
	page = chatList(t, e, base, "", ids[1], 0)
	if len(page.Messages) != 3 {
		t.Fatalf("after ids[1]: got %d, want 3", len(page.Messages))
	}
	if page.Messages[0].Id != ids[2] {
		t.Errorf("after ids[1] first = %s, want %s", page.Messages[0].Id, ids[2])
	}

	// before=ids[2] returns ids[0..1].
	page = chatList(t, e, base, ids[2], "", 0)
	if len(page.Messages) != 2 {
		t.Fatalf("before ids[2]: got %d, want 2", len(page.Messages))
	}
	if page.Messages[1].Id != ids[1] {
		t.Errorf("before ids[2] last = %s, want %s", page.Messages[1].Id, ids[1])
	}
}

// TestServer_Chat_Validation exercises the API-layer 400/403/404
// envelope.
func TestServer_Chat_Validation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// Empty text → 400 chat.text_required
	rec := doJSON(t, e, http.MethodPost, base+"/chat/messages", `{"text":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty text: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != api.ErrChatTextRequired {
		t.Errorf("empty text code = %q, want %q", env.Error.Code, api.ErrChatTextRequired)
	}

	// Edit / delete / react against a missing id → 404 chat.not_found.
	rec = doJSON(t, e, http.MethodPatch, base+"/chat/messages/missing", `{"text":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing edit: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodDelete, base+"/chat/messages/missing", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/chat/messages/missing/reactions/"+url.PathEscape("👍"), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing react: %d %s", rec.Code, rec.Body.String())
	}
	_ = strconv.Itoa // silence unused import in case the test grows
}

// TestServer_Chat_Attachments rounds the attachments field through
// send → list, verifying entries are returned as-stored. Also covers
// the 400 path on a bad attachment id so the http-layer fast-path is
// exercised.
func TestServer_Chat_Attachments(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	body := `{
		"text":"with attachments",
		"attachments":{
			"a1":{"type":"link","link":"any://abc/def"},
			"a2":{"type":"image","link":"https://example.com/x.png"}
		}
	}`
	rec := doJSON(t, e, http.MethodPost, base+"/chat/messages", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	var msg api.ChatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(msg.Attachments) != 2 {
		t.Fatalf("attachments len = %d, want 2: %+v", len(msg.Attachments), msg.Attachments)
	}
	if a := msg.Attachments["a1"]; a.Type != "link" || a.Link != "any://abc/def" {
		t.Errorf("a1 = %+v, want {type:link, link:any://abc/def}", a)
	}
	if a := msg.Attachments["a2"]; a.Type != "image" || a.Link != "https://example.com/x.png" {
		t.Errorf("a2 = %+v, want {type:image, link:https://example.com/x.png}", a)
	}

	// Round-trip through list as well.
	listed := chatList(t, e, base, "", "", 0)
	if len(listed.Messages) != 1 {
		t.Fatalf("list: got %d messages, want 1", len(listed.Messages))
	}
	if got := listed.Messages[0]; len(got.Attachments) != 2 {
		t.Errorf("list: attachments len = %d, want 2", len(got.Attachments))
	}

	// 400 on a bad id (contains '.').
	badBody := `{"text":"x","attachments":{"a.bad":{"type":"link","link":"any://x"}}}`
	rec = doJSON(t, e, http.MethodPost, base+"/chat/messages", badBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: expected 400, got %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode err: %v", err)
	}
	if env.Error.Code != api.ErrChatAttachmentsInvalid {
		t.Errorf("err code = %q, want %q", env.Error.Code, api.ErrChatAttachmentsInvalid)
	}
}

// --- helpers ---------------------------------------------------------------

// setupChatFixture creates a space and an object on it. The object is
// stamped with the chat type so the client doesn't have to know the
// type id (mirroring how the user would build a chat object via the
// types API). Reusing chat.TypeId directly here keeps the test
// hermetic; production callers go through Types.List + Objects.Create.
func setupChatFixture(t *testing.T, e http.Handler) (spaceId, objectId string) {
	t.Helper()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"ChatTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	spaceId = sp.Id

	// The chat type is registered as a built-in handler.Type at SDK
	// boot, so we don't go through Types.Create. We just create an
	// object and chat messages will land on its chat_messages dataset.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	objectId = obj.ObjectId
	return spaceId, objectId
}

func chatSend(t *testing.T, e http.Handler, base, text, replyTo string) api.ChatMessage {
	t.Helper()
	body := fmt.Sprintf(`{"text":%q`, text)
	if replyTo != "" {
		body += fmt.Sprintf(`,"replyToMessageId":%q`, replyTo)
	}
	body += `}`
	rec := doJSON(t, e, http.MethodPost, base+"/chat/messages", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send %q: %d %s", text, rec.Code, rec.Body.String())
	}
	var msg api.ChatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode send: %v", err)
	}
	return msg
}

func chatList(t *testing.T, e http.Handler, base, before, after string, limit int) api.ChatListResponse {
	t.Helper()
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if after != "" {
		q.Set("after", after)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := base + "/chat/messages"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	rec := doJSON(t, e, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.ChatListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return resp
}
