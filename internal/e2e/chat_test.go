// Single-binary chat coverage. internal/server/handlers_chat_test.go
// already exercises send/edit/delete/react/list against an in-process
// echo handler with a real SDK; what's missing is a smoke test that
// drives the same endpoints through the actual built `any` binary
// over a real TCP socket. That covers route registration, json
// encoder wiring, and (most importantly) the path-encoded emoji
// segment going through Go's net/http server intact.
package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_ChatBinary drives chat through the real binary: send three
// messages, list, edit, react (toggle on/off), delete, list again.
// Mirrors TestServer_Chat_RoundTrip but through the binary's listener
// rather than echo's in-process handler — catches any divergence in
// route wiring or content-type negotiation.
func TestE2E_ChatBinary(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	// Sanity-check /v1/account is alive — the binary is wired.
	// Note: AccountResponse.Id is libp2p-PeerId encoded
	// (sdk.Account().Id() → SignKey.PeerId()), whereas chat record
	// `creator` is StrKey/Account-encoded (set from
	// crdt.Change.Identity.Account()). Different string formats of the
	// same underlying public key — do NOT compare them with ==.
	var acc api.AccountResponse
	mustJSON(t, http.MethodGet, base+"/v1/account", "", http.StatusOK, &acc)
	if acc.Id == "" {
		t.Fatalf("empty account id")
	}

	// Bootstrap: space + chat object. The chat handler is dataset-
	// scoped (chat_messages), so the object doesn't need an explicit
	// type — every object can receive chat messages.
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"chat-binary"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	chatBase := base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	// Send three messages.
	var first, second, third api.ChatMessage
	mustJSON(t, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"first"}`, http.StatusCreated, &first)
	mustJSON(t, http.MethodPost, chatBase+"/chat/messages",
		fmt.Sprintf(`{"text":"second","replyToMessageId":%q}`, first.Id),
		http.StatusCreated, &second)
	mustJSON(t, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"third"}`, http.StatusCreated, &third)
	creator := first.Creator
	if creator == "" {
		t.Fatalf("creator unstamped: %+v", first)
	}
	for _, m := range []api.ChatMessage{first, second, third} {
		if m.Id == "" {
			t.Fatalf("message id empty: %+v", m)
		}
		if m.Creator != creator {
			t.Errorf("creator drift between messages: %q vs %q", m.Creator, creator)
		}
		if m.CreatedAt == 0 || m.ModifiedAt != m.CreatedAt {
			t.Errorf("timestamps: %+v", m)
		}
	}
	if second.ReplyToMessageId != first.Id {
		t.Errorf("second.replyToMessageId = %q, want %q", second.ReplyToMessageId, first.Id)
	}

	// List returns three in send order.
	var list api.ChatListResponse
	mustJSON(t, http.MethodGet, chatBase+"/chat/messages", "", http.StatusOK, &list)
	if len(list.Messages) != 3 {
		t.Fatalf("list = %d, want 3", len(list.Messages))
	}
	if list.Messages[0].Text != "first" || list.Messages[2].Text != "third" {
		t.Errorf("list order = %s,%s,%s", list.Messages[0].Text,
			list.Messages[1].Text, list.Messages[2].Text)
	}

	// Edit second.
	var edited api.ChatMessage
	mustJSON(t, http.MethodPatch, chatBase+"/chat/messages/"+second.Id,
		`{"text":"second-edited"}`, http.StatusOK, &edited)
	if edited.Text != "second-edited" {
		t.Errorf("edited.Text = %q", edited.Text)
	}
	if edited.ModifiedAt < edited.CreatedAt {
		t.Errorf("modifiedAt %d < createdAt %d", edited.ModifiedAt, edited.CreatedAt)
	}

	// React twice with the same emoji → toggle off. Path encoding for
	// 👍 is what we're really testing here — a multibyte emoji has to
	// round-trip through net/http's path matcher and echo's :emoji
	// param without mojibake.
	emoji := url.PathEscape("👍")
	var rxResp api.ChatReactionsResponse
	mustJSON(t, http.MethodPost, chatBase+"/chat/messages/"+second.Id+"/reactions/"+emoji,
		"", http.StatusOK, &rxResp)
	if len(rxResp.Reactions["👍"]) != 1 || rxResp.Reactions["👍"][0] != creator {
		t.Errorf("after add: reactions = %+v, want {👍:[%s]}", rxResp.Reactions, creator)
	}
	mustJSON(t, http.MethodPost, chatBase+"/chat/messages/"+second.Id+"/reactions/"+emoji,
		"", http.StatusOK, &rxResp)
	if got := rxResp.Reactions["👍"]; len(got) != 0 {
		t.Errorf("after toggle off: reactions[👍] = %v, want empty", got)
	}

	// Delete first.
	mustStatus(t, http.MethodDelete, chatBase+"/chat/messages/"+first.Id, "", http.StatusNoContent)

	// List after delete: two messages, no `first`.
	mustJSON(t, http.MethodGet, chatBase+"/chat/messages", "", http.StatusOK, &list)
	if len(list.Messages) != 2 {
		t.Fatalf("list after delete = %d, want 2", len(list.Messages))
	}
	for _, m := range list.Messages {
		if m.Id == first.Id {
			t.Errorf("deleted message %s still in list", first.Id)
		}
	}

	// Validation envelope: empty text → 400 chat.text_required.
	var env api.ErrorEnvelope
	mustJSON(t, http.MethodPost, chatBase+"/chat/messages", `{"text":""}`,
		http.StatusBadRequest, &env)
	if env.Error.Code != api.ErrChatTextRequired {
		t.Errorf("empty text: code = %q, want %q", env.Error.Code, api.ErrChatTextRequired)
	}

	// 404 envelope: unknown msgId on PATCH/DELETE/REACT.
	mustJSON(t, http.MethodPatch, chatBase+"/chat/messages/missing",
		`{"text":"x"}`, http.StatusNotFound, &env)
	if env.Error.Code != api.ErrChatNotFound {
		t.Errorf("missing edit: code = %q, want %q", env.Error.Code, api.ErrChatNotFound)
	}
	mustJSON(t, http.MethodDelete, chatBase+"/chat/messages/missing", "",
		http.StatusNotFound, &env)
	if env.Error.Code != api.ErrChatNotFound {
		t.Errorf("missing delete: code = %q", env.Error.Code)
	}
	mustJSON(t, http.MethodPost, chatBase+"/chat/messages/missing/reactions/"+emoji, "",
		http.StatusNotFound, &env)
	if env.Error.Code != api.ErrChatNotFound {
		t.Errorf("missing react: code = %q", env.Error.Code)
	}

	// Pagination semantics live in TestServer_Chat_Pagination — the
	// binary smoke deliberately stops here so any-store's limit/sort
	// path is exercised in one place, not two.

}
