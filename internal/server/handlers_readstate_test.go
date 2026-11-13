package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/chat"
)

// TestServer_ReadState_Unread walks the unread feed on one account: an
// agent's done:true message counts unread for its own author, so the
// feed has entries without a second peer.
func TestServer_ReadState_Unread(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, chatA := setupChatFixture(t, e)
	chatB := mustCreateModuleObject(t, e, spaceId, "chat")
	path := "/v1/spaces/" + spaceId + "/read-state/unread"

	unread := func(cursor string, limit int) api.UnreadResponse {
		t.Helper()
		q := url.Values{}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		if limit > 0 {
			q.Set("limit", strconv.Itoa(limit))
		}
		rec := doJSON(t, e, http.MethodGet, path+"?"+q.Encode(), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("unread: %d %s", rec.Code, rec.Body.String())
		}
		var out api.UnreadResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode unread: %v", err)
		}
		return out
	}
	messages := func(res api.UnreadResponse) []string {
		var ids []string
		for _, ch := range res.Changes {
			ids = append(ids, ch.RecordIds...)
		}
		return ids
	}
	send := func(chatId, text string) string {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+chatId+"/chat/messages",
			`{"text":"`+text+`","agent":{"name":"bao","done":true}}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
		}
		return decodeModifyResult(t, rec.Body.Bytes()).RecordIds[0]
	}

	// Unread before the first call is not news.
	send(chatA, "old")
	start := unread("", 0)
	if len(start.Changes) != 0 || start.Cursor == "" {
		t.Fatalf("first call = %+v, want no changes and a cursor", start)
	}

	a1 := send(chatA, "a1")
	b1 := send(chatB, "b1")
	a2 := send(chatA, "a2")
	page := unread(start.Cursor, 0)
	if got, want := messages(page), []string{a1, b1, a2}; !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v (arrival order across chats)", got, want)
	}
	first := page.Changes[0]
	if first.ObjectId != chatA || first.Dataset != chat.Dataset || first.ChangeId == "" ||
		!slices.Equal(first.Tags, []string{chat.TagMessage}) {
		t.Errorf("change = %+v", first)
	}
	if page.Changes[1].ObjectId != chatB {
		t.Errorf("second change object = %s, want %s", page.Changes[1].ObjectId, chatB)
	}
	if again := unread(page.Cursor, 0); len(again.Changes) != 0 || again.Cursor != page.Cursor {
		t.Errorf("resume = %+v, want no changes and cursor %s", again, page.Cursor)
	}

	// Paging resumes on the returned cursor.
	p1 := unread(start.Cursor, 2)
	if got, want := messages(p1), []string{a1, b1}; !slices.Equal(got, want) {
		t.Fatalf("page 1 = %v, want %v", got, want)
	}
	p2 := unread(p1.Cursor, 2)
	if got, want := messages(p2), []string{a2}; !slices.Equal(got, want) || p2.Cursor != page.Cursor {
		t.Fatalf("page 2 = %v cursor %s, want %v cursor %s", got, p2.Cursor, want, page.Cursor)
	}

	// Read changes drop out.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+chatA+"/chat/read-all", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("read-all: %d %s", rec.Code, rec.Body.String())
	}
	if got, want := messages(unread(start.Cursor, 0)), []string{b1}; !slices.Equal(got, want) {
		t.Errorf("after read-all = %v, want %v", got, want)
	}

	// A cursor from another store generation starts at the present.
	stale := unread(formatReadCursor(space.ReadCursor{Generation: "0000", StateSeq: 1}), 0)
	if len(stale.Changes) != 0 || stale.Cursor == "" {
		t.Errorf("stale cursor = %+v, want no changes and a fresh cursor", stale)
	}
	if left := unread(stale.Cursor, 0); len(left.Changes) != 0 {
		t.Errorf("after a fresh cursor = %v, want nothing", messages(left))
	}

	for _, bad := range []string{
		"?cursor=nodot", "?cursor=gen.x", "?cursor=.5", "?cursor=gen.-1",
		"?cursor=gen.9223372036854775808", // past the seq axis
		"?limit=0", "?limit=x",
	} {
		rec := doJSON(t, e, http.MethodGet, path+bad, "")
		var env api.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != http.StatusBadRequest || env.Error.Code != "request.invalid_field" {
			t.Errorf("%s: %d %s, want 400 request.invalid_field", bad, rec.Code, rec.Body.String())
		}
	}

	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+acc.TechSpaceId+"/read-state/unread", "")
	var env api.ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if rec.Code != http.StatusMethodNotAllowed || env.Error.Code != "space.unsupported" {
		t.Errorf("tech space: %d %s, want 405 space.unsupported", rec.Code, rec.Body.String())
	}
}

func TestParseReadLimit(t *testing.T) {
	for raw, want := range map[string]int{"": 100, "1": 1, "1000": 1000, "5000": 1000} {
		if got, ok := parseReadLimit(raw); !ok || got != want {
			t.Errorf("limit %q = %d, %v, want %d", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"0", "-1", "x", "1.5"} {
		if _, ok := parseReadLimit(raw); ok {
			t.Errorf("limit %q accepted", raw)
		}
	}
}

func TestReadCursorRoundTrip(t *testing.T) {
	cur := space.ReadCursor{Generation: "65f0a1b2c3d4e5f601234567", StateSeq: 9223372036854775807}
	got, err := parseReadCursor(formatReadCursor(cur))
	if err != nil || got != cur {
		t.Fatalf("round trip = %+v, %v, want %+v", got, err, cur)
	}
	if zero, err := parseReadCursor(""); err != nil || zero != (space.ReadCursor{}) {
		t.Fatalf("empty = %+v, %v, want the zero cursor", zero, err)
	}
}
