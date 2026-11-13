package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerChatUnreadFeed proves the notification feed
// (GET …/read-state/unread) on an observer: each change that becomes
// unread is returned once, after the cursor, whatever its place in the
// chat's order — messages that merge below ones already seen, a
// mention added by an edit, and one change per reaction. A restart
// keeps the cursor valid.
func TestE2E_MultipeerChatUnreadFeed(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer unread feed test takes minutes; rerun without -short")
	}
	bin := buildBinary(t)
	obs := startPeer(t, bin, "observer")
	defer obs.stop(t)
	w1 := startPeer(t, bin, "writer1")
	defer w1.stop(t)
	w2 := startPeer(t, bin, "writer2")
	defer w2.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, obs.base+"/v1/spaces", `{"name":"unread-feed"}`, http.StatusCreated, &sp)
	chatId := createModuleObject(t, obs.base, sp.Id, "chat")
	chatOn := func(p *peer) string { return p.base + "/v1/spaces/" + sp.Id + "/objects/" + chatId }
	obsBase := chatOn(obs)

	own := sendChat(t, obsBase, `{"text":"observer's own message"}`)

	joinSpace(t, obs, w1, sp.Id, api.SpacePermissionWriter)
	if resp, raw := doRequest(t, http.MethodDelete, obs.base+"/v1/spaces/"+sp.Id+"/invites", ""); resp.StatusCode >= 300 {
		t.Fatalf("revoke invites: %d %s", resp.StatusCode, raw)
	}
	joinSpace(t, obs, w2, sp.Id, api.SpacePermissionWriter)
	everyoneHas := func(what, id string) {
		t.Helper()
		if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{obs, w1, w2}, func() bool {
			for _, p := range []*peer{obs, w1, w2} {
				if findById(chatMessages(t, chatOn(p)), id).Id == "" {
					return false
				}
			}
			return true
		}) {
			t.Fatalf("%s never reached every peer", what)
		}
	}
	everyoneHas("the observer's message", own.Id)
	waitChatDeclared(t, w1.base, sp.Id)
	waitChatDeclared(t, w2.base, sp.Id)

	// A message to edit later, then one from the observer that every
	// peer holds: the single head both writers branch from.
	old := sendChat(t, chatOn(w1), `{"text":"an old message"}`)
	everyoneHas("the old message", old.Id)
	anchor := sendChat(t, obsBase, `{"text":"anchor"}`)
	everyoneHas("the anchor", anchor.Id)
	mustStatus(t, http.MethodPost, obsBase+"/chat/read-all", "", http.StatusNoContent)

	feedPath := obs.base + "/v1/spaces/" + sp.Id + "/read-state/unread"
	feed := func(cursor string) api.UnreadResponse {
		t.Helper()
		var out api.UnreadResponse
		mustJSON(t, http.MethodGet, feedPath+"?cursor="+url.QueryEscape(cursor), "", http.StatusOK, &out)
		return out
	}
	// consume pages the feed from cursor the way a client does, until
	// want changes arrived, and returns them with the next cursor.
	consume := func(what, cursor string, want int, peers ...*peer) ([]api.UnreadChange, string) {
		t.Helper()
		var got []api.UnreadChange
		ok := pollUntilSynced(t, 3*time.Minute, sp.Id, peers, func() bool {
			page := feed(cursor)
			got = append(got, page.Changes...)
			cursor = page.Cursor
			return len(got) >= want
		})
		if !ok || len(got) != want {
			t.Fatalf("%s: feed returned %d changes, want %d: %+v", what, len(got), want, got)
		}
		for _, ch := range got {
			if ch.ObjectId != chatId || ch.Dataset != "chat_messages" || ch.ChangeId == "" {
				t.Fatalf("%s: change = %+v", what, ch)
			}
		}
		return got, cursor
	}
	records := func(changes []api.UnreadChange) []string {
		var ids []string
		for _, ch := range changes {
			ids = append(ids, ch.RecordIds...)
		}
		return ids
	}

	start := feed("")
	if len(start.Changes) != 0 || start.Cursor == "" {
		t.Fatalf("first call = %+v, want no changes and a cursor", start)
	}
	cursor := start.Cursor

	// Both writers write offline: two branches off the anchor. any-sync
	// orders concurrent branches by their first change's id, so the
	// branch with the smaller one sorts first whenever it arrives.
	type branch struct {
		p       *peer
		restore func()
		first   string // id of the branch's first change
		msgs    []string
	}
	branches := []*branch{{p: w1}, {p: w2}}
	for _, b := range branches {
		b.restore = restartOffline(t, bin, b.p)
		assertOffline(t, b.p, sp.Id)
		for i := 0; i < 3; i++ {
			var res api.ModifyResult
			mustJSON(t, http.MethodPost, chatOn(b.p)+"/chat/messages",
				fmt.Sprintf(`{"text":"%s offline %d"}`, b.p.name, i), http.StatusCreated, &res)
			if i == 0 {
				b.first = res.ChangeId
			}
			b.msgs = append(b.msgs, res.RecordIds[0])
		}
	}
	// The branch that sorts last comes back first, so the other one
	// merges below messages the observer already has.
	early, late := branches[0], branches[1]
	if early.first < late.first {
		early, late = late, early
	}

	early.restore()
	got, cursor := consume("first branch", cursor, len(early.msgs), early.p, obs)
	if !slices.Equal(records(got), early.msgs) {
		t.Fatalf("first branch = %v, want %v", records(got), early.msgs)
	}

	late.restore()
	got, cursor = consume("mid-history branch", cursor, len(late.msgs), late.p, obs)
	if !slices.Equal(records(got), late.msgs) {
		t.Fatalf("mid-history branch = %v, want %v", records(got), late.msgs)
	}
	for _, ch := range got {
		if !slices.Equal(ch.Tags, []string{"message"}) {
			t.Errorf("message tags = %v, want [message]", ch.Tags)
		}
	}
	var order []string
	for _, m := range chatMessages(t, obsBase).Messages {
		order = append(order, m.Id)
	}
	if last, first := slices.Index(order, late.msgs[2]), slices.Index(order, early.msgs[0]); last < 0 || last > first {
		t.Fatalf("the second branch did not merge below the first: chat order %v, first branch %v, second %v",
			order, early.msgs, late.msgs)
	}

	// An edit adds a mention to a message already read.
	mustStatus(t, http.MethodPost, obsBase+"/chat/read-all", "", http.StatusNoContent)
	edit := fmt.Sprintf(`{"text":"an old message, now pinging [o](any://u/%s)"}`, own.Creator)
	mustStatus(t, http.MethodPatch, chatOn(w1)+"/chat/messages/"+old.Id, edit, http.StatusOK)
	got, cursor = consume("edit-added mention", cursor, 1, w1, obs)
	if !slices.Equal(got[0].RecordIds, []string{old.Id}) || !slices.Equal(got[0].Tags, []string{"mention"}) {
		t.Fatalf("edit-added mention = %+v, want record %s tagged mention", got[0], old.Id)
	}

	// Each reaction to the observer's own message is one change.
	heart := url.PathEscape("❤️")
	for _, p := range []*peer{w1, w2} {
		mustStatus(t, http.MethodPost, chatOn(p)+"/chat/messages/"+own.Id+"/reactions/"+heart, "", http.StatusOK)
		got, cursor = consume(p.name+"'s reaction", cursor, 1, p, obs)
		if !slices.Equal(got[0].RecordIds, []string{own.Id}) || !slices.Equal(got[0].Tags, []string{"reaction"}) {
			t.Fatalf("%s's reaction = %+v, want record %s tagged reaction", p.name, got[0], own.Id)
		}
	}

	// A restart re-notifies nothing and keeps earlier cursors valid.
	obs.stop(t)
	bootWithConfig(t, bin, obs, fmt.Sprintf("network:\n  nodeconfPath: %s\nindex:\n  embedder: none\n", absStagingPath(t)))
	if after := feed(cursor); len(after.Changes) != 0 || after.Cursor != cursor {
		t.Fatalf("after restart = %+v, want no changes and cursor %s", after, cursor)
	}
	if left := records(feed(start.Cursor).Changes); !slices.Equal(left, []string{old.Id, own.Id, own.Id}) {
		t.Fatalf("still unread since the first cursor = %v, want the mention and both reactions", left)
	}
}
