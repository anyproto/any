package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

	for _, msg := range []chatMsg{first, second, third} {
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
	listed := chatList(t, e, base)
	if len(listed.Messages) != 3 {
		t.Fatalf("list: got %d messages, want 3", len(listed.Messages))
	}
	if listed.Messages[0].Text != "first" || listed.Messages[1].Text != "second" || listed.Messages[2].Text != "third" {
		t.Errorf("list order = [%s, %s, %s], want [first, second, third]",
			listed.Messages[0].Text, listed.Messages[1].Text, listed.Messages[2].Text)
	}

	// Edit second message — write returns a ModifyResult; the edited
	// record is read back via query.
	body := `{"text":"second-edited"}`
	rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+second.Id, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	if res := decodeModifyResult(t, rec.Body.Bytes()); res.VersionId == "" {
		t.Errorf("edit: empty versionId in %+v", res)
	}
	edited := getChatMsg(t, e, base, second.Id)
	if edited.Text != "second-edited" {
		t.Errorf("edited.Text = %q, want second-edited", edited.Text)
	}
	if edited.ModifiedAt == 0 || edited.ModifiedAt < edited.CreatedAt {
		t.Errorf("modifiedAt should be set and >= createdAt: %+v", edited)
	}

	// React with 👍 (add) — write returns a ModifyResult; reactions are
	// read back via query.
	rxRec := doJSON(t, e, http.MethodPost, base+"/chat/messages/"+second.Id+"/reactions/"+url.PathEscape("👍"), "")
	if rxRec.Code != http.StatusOK {
		t.Fatalf("POST react add: %d %s", rxRec.Code, rxRec.Body.String())
	}
	if res := decodeModifyResult(t, rxRec.Body.Bytes()); res.VersionId == "" {
		t.Errorf("react add: empty versionId in %+v", res)
	}
	if reacted := getChatMsg(t, e, base, second.Id); func() bool {
		_, ok := reacted.Reactions["👍"][d.account]
		return len(reacted.Reactions["👍"]) != 1 || !ok
	}() {
		t.Errorf("after add: reactions = %+v, want {👍: {%s: ts}}",
			getChatMsg(t, e, base, second.Id).Reactions, d.account)
	}

	// React again toggles off.
	rxRec = doJSON(t, e, http.MethodPost, base+"/chat/messages/"+second.Id+"/reactions/"+url.PathEscape("👍"), "")
	if rxRec.Code != http.StatusOK {
		t.Fatalf("POST react toggle off: %d %s", rxRec.Code, rxRec.Body.String())
	}
	if got := getChatMsg(t, e, base, second.Id).Reactions["👍"]; len(got) != 0 {
		t.Errorf("after toggle off: reactions[👍] = %v, want empty", got)
	}

	// Delete first message — now returns 200 with a ModifyResult.
	delRec := doJSON(t, e, http.MethodDelete, base+"/chat/messages/"+first.Id, "")
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", delRec.Code, delRec.Body.String())
	}
	if res := decodeModifyResult(t, delRec.Body.Bytes()); res.VersionId == "" {
		t.Errorf("delete: empty versionId in %+v", res)
	}

	// List after delete: two messages remain.
	listed = chatList(t, e, base)
	if len(listed.Messages) != 2 {
		t.Errorf("list after delete: got %d, want 2", len(listed.Messages))
	}
	for _, m := range listed.Messages {
		if m.Id == first.Id {
			t.Errorf("deleted message %s still in list", first.Id)
		}
	}
}

// Pagination via before/after/limit moved off the API surface — the
// HTTP GET endpoint that exposed it is gone. Clients now paginate by
// chaining /query calls with filters on `_ver.id` (the chronological
// marker). See docs/03-api.md § Chat for the recipe.

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
	res := decodeModifyResult(t, rec.Body.Bytes())
	if len(res.RecordIds) == 0 {
		t.Fatalf("send: no recordIds in %+v", res)
	}
	msg := getChatMsg(t, e, base, res.RecordIds[0])
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
	listed := chatList(t, e, base)
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

// TestServer_Chat_Agent rounds the agent group through send → list,
// verifying it is returned as-stored, plus the 400 fast-path on a
// nameless group.
func TestServer_Chat_Agent(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	body := `{
		"text":"working on it",
		"agent":{"name":"bao","debugLink":"any://sp/dbg#turn_2","done":false}
	}`
	rec := doJSON(t, e, http.MethodPost, base+"/chat/messages", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if len(res.RecordIds) == 0 {
		t.Fatalf("send: no recordIds in %+v", res)
	}
	msg := getChatMsg(t, e, base, res.RecordIds[0])
	if msg.Agent == nil {
		t.Fatalf("agent missing on read-back: %+v", msg)
	}
	if msg.Agent.Name != "bao" || msg.Agent.DebugLink != "any://sp/dbg#turn_2" || msg.Agent.Done {
		t.Errorf("agent = %+v, want {bao any://sp/dbg#turn_2 false}", msg.Agent)
	}

	// Minimal group — debugLink omitted.
	rec = doJSON(t, e, http.MethodPost, base+"/chat/messages", `{"text":"done","agent":{"name":"bao","done":true}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send minimal: %d %s", rec.Code, rec.Body.String())
	}
	res = decodeModifyResult(t, rec.Body.Bytes())
	msg = getChatMsg(t, e, base, res.RecordIds[0])
	if msg.Agent == nil || msg.Agent.Name != "bao" || msg.Agent.DebugLink != "" || !msg.Agent.Done {
		t.Errorf("minimal agent = %+v, want {bao  true}", msg.Agent)
	}

	// Human message — no agent on read-back.
	rec = doJSON(t, e, http.MethodPost, base+"/chat/messages", `{"text":"hi"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send human: %d %s", rec.Code, rec.Body.String())
	}
	res = decodeModifyResult(t, rec.Body.Bytes())
	if msg = getChatMsg(t, e, base, res.RecordIds[0]); msg.Agent != nil {
		t.Errorf("human message carries agent: %+v", msg.Agent)
	}

	// 400 on a nameless group.
	rec = doJSON(t, e, http.MethodPost, base+"/chat/messages", `{"text":"x","agent":{"done":true}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nameless agent: expected 400, got %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode err: %v", err)
	}
	if env.Error.Code != api.ErrChatAgentInvalid {
		t.Errorf("err code = %q, want %q", env.Error.Code, api.ErrChatAgentInvalid)
	}
}

// TestServer_Chat_ReactionsRead_NoOp exercises the reactions-read route
// against a real single-account SDK. Own reactions are born read
// (docs/16-chat.md), so single-account this is genuinely the no-op path:
// the message has no unread reaction to clear. The route must still
// return 204 (idempotent contract — no 404) and must not move the chat
// row's unreadReactionsCount off zero. The cross-account clearing proof
// lives in internal/e2e (TestE2E_ChatReactionsRead) — a single account
// can't hold an unread reaction. Also covers the objectId/msgId param
// guards, which need a real SDK because resolveSpace runs first.
func TestServer_Chat_ReactionsRead_NoOp(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	msg := chatSend(t, e, base, "hello", "")

	// No-op reactions-read: 204, and the reaction counter stays 0.
	rec := doJSON(t, e, http.MethodPost, base+"/chat/messages/"+msg.Id+"/reactions-read", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reactions-read: %d %s", rec.Code, rec.Body.String())
	}
	if got := chatRowUnreadReactions(t, e, spaceId, objectId); got != 0 {
		t.Errorf("unreadReactionsCount = %d after no-op reactions-read, want 0", got)
	}

	// Idempotent: a second call is also a clean 204.
	rec = doJSON(t, e, http.MethodPost, base+"/chat/messages/"+msg.Id+"/reactions-read", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second reactions-read: %d %s", rec.Code, rec.Body.String())
	}

	// Param guards (resolveSpace succeeds on the real space, then the
	// handler's own objectId/msgId check fires). Echo matches empty
	// middle path params, so these reach the handler.
	rec = doJSON(t, e, http.MethodPost,
		"/v1/spaces/"+spaceId+"/objects//chat/messages/"+msg.Id+"/reactions-read", "")
	if rec.Code != http.StatusBadRequest || errEnvCode(t, rec.Body.Bytes()) != "request.missing_field" {
		t.Errorf("empty objectId: got %d %s, want 400 request.missing_field", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost,
		base+"/chat/messages//reactions-read", "")
	if rec.Code != http.StatusBadRequest || errEnvCode(t, rec.Body.Bytes()) != "request.missing_field" {
		t.Errorf("empty msgId: got %d %s, want 400 request.missing_field", rec.Code, rec.Body.String())
	}
}

// TestServer_Chat_ReactionsRead_Routing proves the route is registered
// without a live SDK: with a bare deps (ready=true clears the auth
// guard) an empty spaceId reaches resolveSpace and returns 400
// request.missing_field — a 404 would mean the route isn't wired. Never
// skips, so it guards the wiring even when the staging fixture is
// absent. Mirrors the handlers_onetoone_test.go pattern.
func TestServer_Chat_ReactionsRead_Routing(t *testing.T) {
	d := &deps{}
	d.ready.Store(true)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost,
		"/v1/spaces//objects/o1/chat/messages/m1/reactions-read", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "request.missing_field" {
		t.Errorf("code = %q, want request.missing_field", got)
	}
}

// chatRowUnreadReactions reads the chat object's row via POST
// /objects/query and returns its unreadReactionsCount counter (stored
// under the chat type's xKey). Absent decodes as 0.
func chatRowUnreadReactions(t *testing.T, e http.Handler, spaceId, objectId string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"filter": map[string]any{"id": objectId}})
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/query", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("objects/query: %d %s", rec.Code, rec.Body.String())
	}
	var qr struct {
		Records []struct {
			Chat struct {
				UnreadReactionsCount float64 `json:"unreadReactionsCount"`
			} `json:"chat"`
		} `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode objects/query: %v", err)
	}
	if len(qr.Records) == 0 {
		return 0
	}
	return int(qr.Records[0].Chat.UnreadReactionsCount)
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

// chatMsg is the test-local decode target for a chat_messages record
// read back via /query. Writes now return api.ModifyResult, so the
// message body is always fetched through the query path.
type chatMsg struct {
	Id               string
	Creator          string
	CreatedAt        int64
	ModifiedAt       int64
	ReplyToMessageId string
	Agent            *api.ChatAgentMeta
	Text             string
	Attachments      map[string]api.ChatAttachment
	Reactions        map[string]map[string]int64
}

// chatSend posts a message and returns the read-back record. recordIds[0]
// from the ModifyResult is the server-derived message id, which we
// re-query to surface creator/createdAt/replyTo/etc.
func chatSend(t *testing.T, e http.Handler, base, text, replyTo string) chatMsg {
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
	res := decodeModifyResult(t, rec.Body.Bytes())
	if res.VersionId == "" {
		t.Errorf("send %q: empty versionId in %+v", text, res)
	}
	if len(res.RecordIds) == 0 || res.RecordIds[0] == "" {
		t.Fatalf("send %q: no recordIds in %+v", text, res)
	}
	return getChatMsg(t, e, base, res.RecordIds[0])
}

// decodeModifyResult decodes a write response into api.ModifyResult.
func decodeModifyResult(t *testing.T, raw []byte) api.ModifyResult {
	t.Helper()
	var res api.ModifyResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode ModifyResult: %v\nraw=%s", err, raw)
	}
	return res
}

// getChatMsg queries one message by id and returns it; fails if absent.
func getChatMsg(t *testing.T, e http.Handler, base, id string) chatMsg {
	t.Helper()
	for _, m := range chatList(t, e, base).Messages {
		if m.Id == id {
			return m
		}
	}
	t.Fatalf("getChatMsg: message %s not found", id)
	return chatMsg{}
}

// chatListResp is the read-back message list, materialised via POST
// /v1/spaces/:id/query with dataset=chat_messages.
type chatListResp struct {
	Messages []chatMsg
}

func chatList(t *testing.T, e http.Handler, base string) chatListResp {
	t.Helper()
	parts := strings.SplitN(strings.TrimPrefix(base, "/v1/spaces/"), "/objects/", 2)
	if len(parts) != 2 {
		t.Fatalf("chatList: cannot parse spaceId/objectId from %q", base)
	}
	spaceId, objectId := parts[0], parts[1]
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId,
		"dataset":  "chat_messages",
		"sort":     []string{"_ver.id"},
	})
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("query chat: %d %s", rec.Code, rec.Body.String())
	}
	var qr struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	out := chatListResp{Messages: make([]chatMsg, 0, len(qr.Records))}
	for _, raw := range qr.Records {
		out.Messages = append(out.Messages, decodeChatMsg(t, raw))
	}
	return out
}

// decodeChatMsg rehydrates one record from the query wire shape into
// chatMsg. anyenc → fastjson renders numeric fields as JSON numbers in
// exponential form for large ints (`1.78e+09`), which Go's
// encoding/json can't unmarshal into int64, so we hop through float64.
// Reactions ship in storage layout (emoji → {accountId: ts}) — the same
// shape the write path no longer transposes.
func decodeChatMsg(t *testing.T, raw []byte) chatMsg {
	t.Helper()
	var f struct {
		Id               string                        `json:"id"`
		Creator          string                        `json:"creator"`
		CreatedAt        float64                       `json:"createdAt"`
		ModifiedAt       float64                       `json:"modifiedAt"`
		ReplyToMessageId string                        `json:"replyToMessageId"`
		Agent            *api.ChatAgentMeta            `json:"agent"`
		Text             string                        `json:"text"`
		Attachments      map[string]api.ChatAttachment `json:"attachments"`
		Reactions        map[string]map[string]float64 `json:"reactions"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode message: %v\nraw=%s", err, raw)
	}
	var reactions map[string]map[string]int64
	if len(f.Reactions) > 0 {
		reactions = make(map[string]map[string]int64, len(f.Reactions))
		for emoji, byAcct := range f.Reactions {
			inner := make(map[string]int64, len(byAcct))
			for acct, ts := range byAcct {
				inner[acct] = int64(ts)
			}
			reactions[emoji] = inner
		}
	}
	return chatMsg{
		Id:               f.Id,
		Creator:          f.Creator,
		CreatedAt:        int64(f.CreatedAt),
		ModifiedAt:       int64(f.ModifiedAt),
		ReplyToMessageId: f.ReplyToMessageId,
		Agent:            f.Agent,
		Text:             f.Text,
		Attachments:      f.Attachments,
		Reactions:        reactions,
	}
}
