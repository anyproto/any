// Multipeer mentions coverage — the acceptance test for SYN-72.
//
// The derived `mentions` array itself is covered single-process in
// internal/server (handlers_chat_mentions_test.go); what only a
// multi-account test can reach is the badging seam: the recipient's
// replica deriving `mentions` from a sync-ingested change and its
// classifier flipping `unreadMention` + `chat.unreadMentions` for the
// mentioned account — and for nobody else.
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerChatMentions drives the mention badging story:
//
//  1. A message with a mention link (any://m/<sp>/<identity>) badges
//     the mentioned account: `mentions` converges on the recipient,
//     `unreadMention` sets alongside `unread`, and the row counters
//     move together (unreadCount AND unreadMentions).
//  2. A plain cross-account message moves only unreadCount.
//  3. A reply folds the replied-to author into `mentions` at write
//     time and badges them — no link in the text.
//  4. read-all clears the mention flag and counter.
//  5. An edit that adds a mention (re-)badges the mentioned account
//     WITHOUT re-flagging `unread` — mentions ride their own tag.
//  6. Self-mentions never badge the author (born read).
func TestE2E_MultipeerChatMentions(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer mentions test takes ~3min; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"chat-mentions"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	ownerBase := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId
	joinerBase := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	var joinerAcc api.AccountResponse
	mustJSON(t, http.MethodGet, joiner.base+"/v1/account", "", http.StatusOK, &joinerAcc)
	joinerId := joinerAcc.Id

	// Pre-join message (attaches the chat type), then join + baseline.
	m1 := sendChat(t, ownerBase, `{"text":"pre-join history"}`)
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		return findById(chatMessages(t, joinerBase), m1.Id).Id == m1.Id
	}) {
		t.Fatalf("joiner never converged on the pre-join message")
	}
	mustStatus(t, http.MethodPost, joinerBase+"/chat/read-all", "", http.StatusNoContent)

	// --- (1) Text mention badges the mentioned account.
	mention := fmt.Sprintf(`{"text":"hey [you](any://m/%s/%s), look at this"}`, sp.Id, joinerId)
	m2 := sendChat(t, ownerBase, mention)
	if len(m2.Mentions) != 1 || m2.Mentions[0] != joinerId {
		t.Fatalf("sender-side mentions = %v, want [%s]", m2.Mentions, joinerId)
	}
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		got := findById(chatMessages(t, joinerBase), m2.Id)
		return len(got.Mentions) == 1 && got.Mentions[0] == joinerId
	}) {
		t.Fatalf("joiner never converged on the derived mentions array")
	}
	var lastMsg chatMsg
	var lastCounters rowCounters
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, joinerBase), m2.Id)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return lastMsg.Unread && lastMsg.UnreadMention &&
			lastCounters.messages == 1 && lastCounters.mentions == 1
	}) {
		t.Fatalf("joiner: mention never badged: unread=%v unreadMention=%v counters=%v",
			lastMsg.Unread, lastMsg.UnreadMention, lastCounters)
	}

	// --- (2) A plain message moves only the message counter.
	m3 := sendChat(t, ownerBase, `{"text":"no pings here"}`)
	if !pollUntil(2*time.Minute, func() bool {
		lastMsg = findById(chatMessages(t, joinerBase), m3.Id)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return lastMsg.Unread && lastCounters.messages == 2
	}) {
		t.Fatalf("joiner: plain message never went unread: counters=%v", lastCounters)
	}
	if lastMsg.UnreadMention || lastCounters.mentions != 1 {
		t.Errorf("joiner: plain message must not badge mentions: m3.unreadMention=%v counters=%v",
			lastMsg.UnreadMention, lastCounters)
	}

	// --- (4) read-all clears flag + counter.
	mustStatus(t, http.MethodPost, joinerBase+"/chat/read-all", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, joinerBase), m2.Id)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return !lastMsg.UnreadMention && lastCounters.mentions == 0 && lastCounters.messages == 0
	}) {
		t.Fatalf("joiner: read-all never cleared the mention: counters=%v", lastCounters)
	}

	// --- (3) Reply fold-in: the owner replying to the JOINER's message
	// badges the joiner with no link in the text.
	m4 := sendChat(t, joinerBase, `{"text":"from the joiner"}`)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{joiner, owner}, func() bool {
		return findById(chatMessages(t, ownerBase), m4.Id).Id == m4.Id
	}) {
		t.Fatalf("owner never converged on the joiner's message")
	}
	m5 := sendChat(t, ownerBase, fmt.Sprintf(`{"text":"replying","replyToMessageId":%q}`, m4.Id))
	if len(m5.Mentions) != 1 || m5.Mentions[0] != joinerId {
		t.Fatalf("reply fold-in on sender: mentions = %v, want [%s]", m5.Mentions, joinerId)
	}
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		got := findById(chatMessages(t, joinerBase), m5.Id)
		return len(got.Mentions) == 1 && got.Mentions[0] == joinerId
	}) {
		t.Fatalf("joiner never converged on the reply's folded-in mention")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, joinerBase), m5.Id)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return lastMsg.UnreadMention && lastCounters.mentions == 1
	}) {
		t.Fatalf("joiner: reply fold-in never badged: unreadMention=%v counters=%v",
			lastMsg.UnreadMention, lastCounters)
	}
	mustStatus(t, http.MethodPost, joinerBase+"/chat/read-all", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		return chatRowCounters(t, joiner, sp.Id, obj.ObjectId).mentions == 0
	}) {
		t.Fatalf("joiner: read-all never cleared the reply mention")
	}

	// --- (5) An edit that adds a mention badges WITHOUT re-flagging
	// unread: the mention counter moves, the message counter doesn't.
	editBody := fmt.Sprintf(`{"text":"edited to ping [you](any://m/%s/%s)"}`, sp.Id, joinerId)
	mustStatus(t, http.MethodPatch, ownerBase+"/chat/messages/"+m3.Id, editBody, http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		got := findById(chatMessages(t, joinerBase), m3.Id)
		return len(got.Mentions) == 1 && got.Mentions[0] == joinerId
	}) {
		t.Fatalf("joiner never converged on the edit-added mention")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, joinerBase), m3.Id)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return lastMsg.UnreadMention && lastCounters.mentions == 1
	}) {
		t.Fatalf("joiner: edit-added mention never badged: counters=%v", lastCounters)
	}
	if lastMsg.Unread || lastCounters.messages != 0 {
		t.Errorf("joiner: an edit must not re-flag unread: m3.unread=%v counters=%v",
			lastMsg.Unread, lastCounters)
	}

	// --- (6) Self-mentions never badge the author: the owner mentioned
	// nobody but the joiner all along, so its own counters are clean;
	// pin it with an explicit self-mention.
	var ownerAcc api.AccountResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/account", "", http.StatusOK, &ownerAcc)
	selfPing := fmt.Sprintf(`{"text":"note to [self](any://m/%s/%s)"}`, sp.Id, ownerAcc.Id)
	m6 := sendChat(t, ownerBase, selfPing)
	if len(m6.Mentions) != 1 || m6.Mentions[0] != ownerAcc.Id {
		t.Fatalf("self-mention array = %v, want [%s] (objective fact)", m6.Mentions, ownerAcc.Id)
	}
	if c := chatRowCounters(t, owner, sp.Id, obj.ObjectId); c.mentions != 0 {
		t.Errorf("owner: self-mention must stay born-read, counters=%v", c)
	}
}
