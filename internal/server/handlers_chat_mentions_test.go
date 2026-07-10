package server

import (
	"fmt"
	"net/http"
	"testing"
)

// TestServer_Chat_Mentions drives the derived `mentions` field through
// a real SDK apply: text-link extraction, the reply fold-in (ctx.Get
// reading the replied-to message's creator), dedup across the two
// sources, edit re-derivation (including the $unset-when-empty rule),
// and the client-supplied rejection.
func TestServer_Chat_Mentions(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// Plain message: no mentions field at all.
	plain := chatSend(t, e, base, "no pings here", "")
	if plain.Mentions != nil {
		t.Errorf("plain message mentions = %v, want absent", plain.Mentions)
	}
	self := plain.Creator // a real ≥40-char identity, safe in a mention link

	// Text mention: extracted from the markdown link destination.
	mentioned := chatSend(t, e, base,
		fmt.Sprintf("hey [you](any://m/%s/%s), look", spaceId, self), "")
	if len(mentioned.Mentions) != 1 || mentioned.Mentions[0] != self {
		t.Errorf("text mention: mentions = %v, want [%s]", mentioned.Mentions, self)
	}

	// Reply fold-in: the replied-to message's creator lands in
	// mentions without any link in the text.
	reply := chatSend(t, e, base, "replying with no links", plain.Id)
	if len(reply.Mentions) != 1 || reply.Mentions[0] != plain.Creator {
		t.Errorf("reply fold-in: mentions = %v, want [%s]", reply.Mentions, plain.Creator)
	}

	// Reply whose text ALSO mentions the replied-to author: one entry.
	both := chatSend(t, e, base,
		fmt.Sprintf("ping [again](any://m/%s/%s)", spaceId, self), plain.Id)
	if len(both.Mentions) != 1 || both.Mentions[0] != self {
		t.Errorf("reply+text dedup: mentions = %v, want [%s]", both.Mentions, self)
	}

	// Edit re-derivation: adding a mention to a plain message stamps
	// the array...
	body := fmt.Sprintf(`{"text":"edited to ping [you](any://m/%s/%s)"}`, spaceId, self)
	if rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+plain.Id, body); rec.Code != http.StatusOK {
		t.Fatalf("edit add mention: %d %s", rec.Code, rec.Body.String())
	}
	if got := getChatMsg(t, e, base, plain.Id); len(got.Mentions) != 1 || got.Mentions[0] != self {
		t.Errorf("edit add: mentions = %v, want [%s]", got.Mentions, self)
	}
	// ...and editing the mention away $unsets it.
	if rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+plain.Id, `{"text":"pings removed"}`); rec.Code != http.StatusOK {
		t.Fatalf("edit remove mention: %d %s", rec.Code, rec.Body.String())
	}
	if got := getChatMsg(t, e, base, plain.Id); got.Mentions != nil {
		t.Errorf("edit remove: mentions = %v, want absent", got.Mentions)
	}

	// Edit of a REPLY re-folds the replied-to creator even when the new
	// text has no links (replyToMessageId is create-only, read from the
	// pre-op record).
	if rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+reply.Id, `{"text":"still a reply"}`); rec.Code != http.StatusOK {
		t.Fatalf("edit reply: %d %s", rec.Code, rec.Body.String())
	}
	if got := getChatMsg(t, e, base, reply.Id); len(got.Mentions) != 1 || got.Mentions[0] != plain.Creator {
		t.Errorf("edit reply re-fold: mentions = %v, want [%s]", got.Mentions, plain.Creator)
	}

	// Client-supplied mentions are inert on the typed send route (the
	// request struct has no such field — unknown JSON keys drop) ...
	rec := doJSON(t, e, http.MethodPost, base+"/chat/messages",
		`{"text":"smuggle","mentions":["someone"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send with unknown field: %d %s", rec.Code, rec.Body.String())
	}
	smuggleId := decodeModifyResult(t, rec.Body.Bytes()).RecordIds[0]
	if got := getChatMsg(t, e, base, smuggleId); got.Mentions != nil {
		t.Errorf("smuggled mentions landed: %v", got.Mentions)
	}
	// ...and rejected outright on the generic modify route — mentions
	// is ScopeDerived (handler-only), so the local-write validation
	// fails fast (400 dataset.validation, change never enters the DAG)
	// and the stored record keeps its derived value.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", fmt.Sprintf(`{
		"objectId": %q, "dataset": "chat_messages",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "mentions", "value": ["forged"]}]}]
	}`, objectId, mentioned.Id))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged $set mentions: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "dataset.validation" {
		t.Errorf("forged $set mentions: code = %q, want dataset.validation", code)
	}
	if got := getChatMsg(t, e, base, mentioned.Id); len(got.Mentions) != 1 || got.Mentions[0] != self {
		t.Errorf("mentions after forged write = %v, want [%s]", got.Mentions, self)
	}

	// Non-mention any:// links (objects, files) never land in mentions.
	other := chatSend(t, e, base,
		fmt.Sprintf("see [doc](any://o/%s/%s) and any://f/%s/file1x", spaceId, objectId, spaceId), "")
	if other.Mentions != nil {
		t.Errorf("non-mention links: mentions = %v, want absent", other.Mentions)
	}
}
